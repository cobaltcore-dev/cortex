// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	. "github.com/majewsky/gg/option"
	liquid "github.com/sapcc/go-api-declarations/liquid"
	"github.com/sapcc/go-bits/must"
)

const (
	// Default URL for the commitments API endpoint.
	// This should match the service name in the helm chart.
	defaultCommitmentsAPIURL = "http://cortex-nova-scheduler:8080"

	// defaultE2EProjectUUID is a well-known fake project UUID used when no ProjectID is configured.
	// It is intentionally not a real OpenStack project — commitments created under it self-expire.
	defaultE2EProjectUUID = "00000000-0000-0000-0000-000000000e2e"
)

// E2EChecksConfig holds the configuration for CR e2e checks.
type E2EChecksConfig struct {
	// BaseURL for the commitments API. If empty, defaults to defaultCommitmentsAPIURL.
	BaseURL string `json:"baseURL"`
	// NoCleanup prevents test commitments from being deleted after a successful run.
	// Useful for local inspection with Tilt. Zero value (false) means cleanup runs normally.
	NoCleanup bool `json:"noCleanup,omitempty"`
	// ProjectID is the OpenStack project UUID for all e2e test commitments.
	// If empty, falls back to RoundTripCheck.TestProjectID, then defaultE2EProjectUUID.
	ProjectID string `json:"projectID,omitempty"`
	// AZs is the list of availability zones to test. If empty, falls back to
	// RoundTripCheck.AZ, then uses "" (any AZ).
	AZs []string `json:"azs,omitempty"`
	// RoundTripCheck holds optional overrides for backward compatibility.
	// Prefer the top-level ProjectID and AZs fields for new configurations.
	RoundTripCheck *E2ERoundTripConfig `json:"roundTripCheck,omitempty"`
}

// E2ERoundTripConfig holds optional overrides for the create→delete round-trip e2e check.
//
// Deprecated: use E2EChecksConfig.ProjectID and E2EChecksConfig.AZs instead.
type E2ERoundTripConfig struct {
	// AZ is the availability zone to use (e.g. "qa-de-1d"). Defaults to "" if not set.
	AZ string `json:"az"`
	// TestProjectID is the OpenStack project UUID to create test commitments under.
	// Defaults to defaultE2EProjectUUID if not set.
	TestProjectID string `json:"testProjectID"`
}

// e2eProjectID returns the effective project UUID for e2e tests.
func e2eProjectID(config E2EChecksConfig) liquid.ProjectUUID {
	if config.ProjectID != "" {
		return liquid.ProjectUUID(config.ProjectID)
	}
	if rt := config.RoundTripCheck; rt != nil && rt.TestProjectID != "" {
		return liquid.ProjectUUID(rt.TestProjectID)
	}
	return liquid.ProjectUUID(defaultE2EProjectUUID)
}

// e2eAZs returns the effective AZ list for e2e tests.
// Falls back to RoundTripCheck.AZ (single AZ), then to [""] (any AZ).
func e2eAZs(config E2EChecksConfig) []liquid.AvailabilityZone {
	if len(config.AZs) > 0 {
		azs := make([]liquid.AvailabilityZone, len(config.AZs))
		for i, az := range config.AZs {
			azs[i] = liquid.AvailabilityZone(az)
		}
		return azs
	}
	if rt := config.RoundTripCheck; rt != nil && rt.AZ != "" {
		return []liquid.AvailabilityZone{liquid.AvailabilityZone(rt.AZ)}
	}
	return []liquid.AvailabilityZone{""}
}

// CheckCommitmentsInfoEndpoint verifies that GET /commitments/v1/info returns 200 with a valid ServiceInfo.
func CheckCommitmentsInfoEndpoint(ctx context.Context, config E2EChecksConfig) {
	baseURL := e2eBaseURL(config)
	apiURL := baseURL + "/commitments/v1/info"
	slog.Info("checking commitments info endpoint", "apiURL", apiURL)

	httpReq := must.Return(http.NewRequestWithContext(ctx, http.MethodGet, apiURL, http.NoBody))
	httpReq.Header.Set("Accept", "application/json")

	//nolint:bodyclose
	resp := must.Return(http.DefaultClient.Do(httpReq))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes := must.Return(io.ReadAll(resp.Body))
		panic(fmt.Sprintf("commitments info API returned status %d: %s", resp.StatusCode, bodyBytes))
	}

	var serviceInfo liquid.ServiceInfo
	if err := json.NewDecoder(resp.Body).Decode(&serviceInfo); err != nil {
		panic(fmt.Sprintf("failed to decode ServiceInfo response: %v", err))
	}

	if serviceInfo.Version < 0 {
		slog.Warn("commitments info returned version -1, knowledge may not be ready yet")
	}
	slog.Info("commitments info endpoint check passed",
		"version", serviceInfo.Version,
		"resourceCount", len(serviceInfo.Resources),
	)
}

// CheckCommitmentsRoundTrip iterates all HandlesCommitments resources from /info and for each (AZ, resource) pair:
//  1. Creates a confirmed test commitment (amount=2, expires in 5 minutes)
//  2. If accepted: calls the usage API to verify it returns a valid response, then deletes the commitment
//  3. If rejected: logs the reason and continues — capacity rejection is not an error
//
// Panics on infrastructure failures (non-200 from the API, deletion failure after acceptance).
func CheckCommitmentsRoundTrip(ctx context.Context, config E2EChecksConfig) {
	baseURL := e2eBaseURL(config)
	projectID := e2eProjectID(config)
	azs := e2eAZs(config)

	serviceInfo := e2eFetchServiceInfo(ctx, baseURL)

	checked := 0
	for _, az := range azs {
		for resourceName, resInfo := range serviceInfo.Resources {
			if !resInfo.HandlesCommitments {
				continue
			}
			e2eRoundTripResource(ctx, baseURL, serviceInfo.Version, az, projectID, resourceName)
			checked++
		}
	}

	if checked == 0 {
		slog.Warn("round-trip check: no HandlesCommitments resources found in /info — nothing checked")
	}
}

// e2eRoundTripResource runs the create→usageCheck→delete cycle for one (AZ, resource) pair.
func e2eRoundTripResource(
	ctx context.Context,
	baseURL string,
	infoVersion int64,
	az liquid.AvailabilityZone,
	projectID liquid.ProjectUUID,
	resourceName liquid.ResourceName,
) {

	testUUID := liquid.CommitmentUUID(fmt.Sprintf("e2e-%d", time.Now().UnixMilli()))
	expiresAt := time.Now().Add(5 * time.Minute)
	const amount = uint64(2)

	createReq := liquid.CommitmentChangeRequest{
		InfoVersion: infoVersion,
		AZ:          az,
		ByProject: map[liquid.ProjectUUID]liquid.ProjectCommitmentChangeset{
			projectID: {
				ByResource: map[liquid.ResourceName]liquid.ResourceCommitmentChangeset{
					resourceName: {
						TotalConfirmedAfter: amount,
						Commitments: []liquid.Commitment{{
							UUID:      testUUID,
							Amount:    amount,
							NewStatus: Some(liquid.CommitmentStatusConfirmed),
							ExpiresAt: expiresAt,
						}},
					},
				},
			},
		},
	}

	slog.Info("round-trip check: creating test commitment",
		"resource", resourceName, "uuid", testUUID, "project", projectID, "az", az)

	rejectionReason := e2eSendChangeCommitments(ctx, baseURL, createReq)
	if rejectionReason != "" {
		// Only capacity rejections (no hosts available) are expected in production clusters.
		// Any other reason (flavor group ineligible, config error, timeout) indicates a
		// regression and should surface as a failure.
		if !strings.Contains(rejectionReason, "no hosts found") {
			panic(fmt.Sprintf("round-trip check: commitment rejected with unexpected reason for resource %s: %s", resourceName, rejectionReason))
		}
		slog.Info("round-trip check: commitment rejected — no capacity, continuing",
			"resource", resourceName, "reason", rejectionReason)
		return
	}
	slog.Info("round-trip check: commitment accepted", "resource", resourceName, "uuid", testUUID)

	// Register cleanup immediately so it runs even if the usage check panics.
	defer func() {
		deleteReq := liquid.CommitmentChangeRequest{
			InfoVersion: infoVersion,
			AZ:          az,
			ByProject: map[liquid.ProjectUUID]liquid.ProjectCommitmentChangeset{
				projectID: {
					ByResource: map[liquid.ResourceName]liquid.ResourceCommitmentChangeset{
						resourceName: {
							TotalConfirmedBefore: amount,
							Commitments: []liquid.Commitment{{
								UUID:      testUUID,
								Amount:    amount,
								OldStatus: Some(liquid.CommitmentStatusConfirmed),
								NewStatus: None[liquid.CommitmentStatus](),
								ExpiresAt: expiresAt,
							}},
						},
					},
				},
			},
		}
		slog.Info("round-trip check: deleting test commitment", "resource", resourceName, "uuid", testUUID)
		if reason := e2eSendChangeCommitments(ctx, baseURL, deleteReq); reason != "" {
			panic(fmt.Sprintf("round-trip check: delete of test commitment %s was rejected: %s", testUUID, reason))
		}
		slog.Info("round-trip check: commitment deleted", "resource", resourceName, "uuid", testUUID)
	}()

	report := e2eFetchUsageReport(ctx, baseURL, az, projectID)
	e2eLogUsageReport(report, az, projectID)
}

// CheckCommitmentsMultiFlavorGroupBatch exercises the pending→batch-confirm flow for each (AZ, resource) pair.
// For each pair it:
//  1. Creates a pending commitment (UUID:A, amount=1, ExpiresAt=10min) — non-confirming, always accepted
//  2. Sends an atomic batch: UUID:A pending→confirmed amount=3, UUID:B new confirmed amount=1
//  3. If the batch is rejected (no capacity): cleans up the pending UUID:A and continues
//  4. If the batch is accepted: parses and logs the usage report; deletes unless NoCleanup=true
//
// This exercises the all-or-nothing semantics of change-commitments: if capacity for the full
// batch (4 units) is unavailable, neither UUID:A nor UUID:B is confirmed.
func CheckCommitmentsMultiFlavorGroupBatch(ctx context.Context, config E2EChecksConfig) {
	baseURL := e2eBaseURL(config)
	projectID := e2eProjectID(config)
	azs := e2eAZs(config)

	serviceInfo := e2eFetchServiceInfo(ctx, baseURL)

	checked := 0
	for _, az := range azs {
		for resourceName, resInfo := range serviceInfo.Resources {
			if !resInfo.HandlesCommitments {
				continue
			}
			e2eBatchFlavorGroupResource(ctx, baseURL, serviceInfo.Version, az, projectID, resourceName, config.NoCleanup)
			checked++
		}
	}

	if checked == 0 {
		slog.Warn("batch check: no HandlesCommitments resources found in /info — nothing checked")
	}
}

// e2eBatchFlavorGroupResource runs the pending→batch-confirm cycle for one (AZ, resource) pair.
func e2eBatchFlavorGroupResource(
	ctx context.Context,
	baseURL string,
	infoVersion int64,
	az liquid.AvailabilityZone,
	projectID liquid.ProjectUUID,
	resourceName liquid.ResourceName,
	noCleanup bool,
) {

	now := time.Now()
	expiresAt := now.Add(10 * time.Minute)
	uuidA := liquid.CommitmentUUID(fmt.Sprintf("e2e-batch-a-%d", now.UnixMilli()))
	uuidB := liquid.CommitmentUUID(fmt.Sprintf("e2e-batch-b-%d", now.UnixMilli()))

	const (
		pendingAmountA   = uint64(1)
		confirmedAmountA = uint64(3)
		confirmedAmountB = uint64(1)
	)

	// Request 1: create UUID:A as pending.
	// Pending creation is non-confirming (totals unchanged) — always accepted, no rejection possible.
	req1 := liquid.CommitmentChangeRequest{
		InfoVersion: infoVersion,
		AZ:          az,
		ByProject: map[liquid.ProjectUUID]liquid.ProjectCommitmentChangeset{
			projectID: {
				ByResource: map[liquid.ResourceName]liquid.ResourceCommitmentChangeset{
					resourceName: {
						Commitments: []liquid.Commitment{{
							UUID:      uuidA,
							Amount:    pendingAmountA,
							NewStatus: Some(liquid.CommitmentStatusPending),
							ExpiresAt: expiresAt,
						}},
					},
				},
			},
		},
	}

	slog.Info("batch check: creating pending commitment",
		"resource", resourceName, "uuid", uuidA, "project", projectID, "az", az)
	if reason := e2eSendChangeCommitments(ctx, baseURL, req1); reason != "" {
		panic(fmt.Sprintf("batch check: unexpected rejection for pending creation of %s: %s", resourceName, reason))
	}
	slog.Info("batch check: pending commitment accepted", "resource", resourceName, "uuid", uuidA)

	// cleanup is updated as the state advances; the deferred call always runs the latest version.
	var cleanup func()
	defer func() {
		if cleanup != nil {
			cleanup()
		}
	}()
	cleanup = func() {
		req := liquid.CommitmentChangeRequest{
			InfoVersion: infoVersion,
			AZ:          az,
			ByProject: map[liquid.ProjectUUID]liquid.ProjectCommitmentChangeset{
				projectID: {
					ByResource: map[liquid.ResourceName]liquid.ResourceCommitmentChangeset{
						resourceName: {
							Commitments: []liquid.Commitment{{
								UUID:      uuidA,
								Amount:    pendingAmountA,
								OldStatus: Some(liquid.CommitmentStatusPending),
								NewStatus: None[liquid.CommitmentStatus](),
								ExpiresAt: expiresAt,
							}},
						},
					},
				},
			},
		}
		slog.Info("batch check: deleting pending commitment", "resource", resourceName, "uuid", uuidA)
		if reason := e2eSendChangeCommitments(ctx, baseURL, req); reason != "" {
			panic(fmt.Sprintf("batch check: delete of pending commitment %s rejected: %s", uuidA, reason))
		}
		slog.Info("batch check: pending commitment deleted", "resource", resourceName, "uuid", uuidA)
	}

	// Request 2: atomic bundle — UUID:A pending→confirmed (grown to amount=3), UUID:B new confirmed amount=1.
	// RequiresConfirmation=true because TotalConfirmed changes from 0 to 4.
	// If capacity for all 4 units is unavailable, the whole bundle is rejected together.
	req2 := liquid.CommitmentChangeRequest{
		InfoVersion: infoVersion,
		AZ:          az,
		ByProject: map[liquid.ProjectUUID]liquid.ProjectCommitmentChangeset{
			projectID: {
				ByResource: map[liquid.ResourceName]liquid.ResourceCommitmentChangeset{
					resourceName: {
						TotalConfirmedBefore: 0,
						TotalConfirmedAfter:  confirmedAmountA + confirmedAmountB,
						Commitments: []liquid.Commitment{
							{
								UUID:      uuidA,
								Amount:    confirmedAmountA,
								OldStatus: Some(liquid.CommitmentStatusPending),
								NewStatus: Some(liquid.CommitmentStatusConfirmed),
								ExpiresAt: expiresAt,
							},
							{
								UUID:      uuidB,
								Amount:    confirmedAmountB,
								NewStatus: Some(liquid.CommitmentStatusConfirmed),
								ExpiresAt: expiresAt,
							},
						},
					},
				},
			},
		},
	}

	slog.Info("batch check: sending batch confirmation",
		"resource", resourceName, "uuidA", uuidA, "uuidB", uuidB,
		"totalConfirmed", confirmedAmountA+confirmedAmountB,
		"project", projectID, "az", az)

	if reason := e2eSendChangeCommitments(ctx, baseURL, req2); reason != "" {
		if !strings.Contains(reason, "no hosts found") {
			panic(fmt.Sprintf("batch check: unexpected rejection for batch of %s: %s", resourceName, reason))
		}
		slog.Info("batch check: batch rejected — no capacity for full amount, cleanup will remove pending",
			"resource", resourceName, "reason", reason)
		return
	}
	slog.Info("batch check: batch confirmed", "resource", resourceName, "uuidA", uuidA, "uuidB", uuidB)

	report := e2eFetchUsageReport(ctx, baseURL, az, projectID)
	e2eLogUsageReport(report, az, projectID)

	if noCleanup {
		slog.Info("batch check: NoCleanup=true, keeping commitments for inspection",
			"resource", resourceName, "uuidA", uuidA, "uuidB", uuidB, "project", projectID)
		cleanup = nil
		return
	}

	// Advance cleanup: delete both confirmed commitments.
	cleanup = func() {
		req := liquid.CommitmentChangeRequest{
			InfoVersion: infoVersion,
			AZ:          az,
			ByProject: map[liquid.ProjectUUID]liquid.ProjectCommitmentChangeset{
				projectID: {
					ByResource: map[liquid.ResourceName]liquid.ResourceCommitmentChangeset{
						resourceName: {
							TotalConfirmedBefore: confirmedAmountA + confirmedAmountB,
							TotalConfirmedAfter:  0,
							Commitments: []liquid.Commitment{
								{
									UUID:      uuidA,
									Amount:    confirmedAmountA,
									OldStatus: Some(liquid.CommitmentStatusConfirmed),
									NewStatus: None[liquid.CommitmentStatus](),
									ExpiresAt: expiresAt,
								},
								{
									UUID:      uuidB,
									Amount:    confirmedAmountB,
									OldStatus: Some(liquid.CommitmentStatusConfirmed),
									NewStatus: None[liquid.CommitmentStatus](),
									ExpiresAt: expiresAt,
								},
							},
						},
					},
				},
			},
		}
		slog.Info("batch check: deleting confirmed commitments", "resource", resourceName, "uuidA", uuidA, "uuidB", uuidB)
		if reason := e2eSendChangeCommitments(ctx, baseURL, req); reason != "" {
			panic(fmt.Sprintf("batch check: cleanup of confirmed commitments %s/%s rejected: %s", uuidA, uuidB, reason))
		}
		slog.Info("batch check: confirmed commitments deleted", "resource", resourceName, "uuidA", uuidA, "uuidB", uuidB)
	}
}

// e2eFetchUsageReport calls POST /commitments/v1/projects/:id/report-usage, decodes the response,
// and returns it. Panics on HTTP errors or decode failures.
func e2eFetchUsageReport(ctx context.Context, baseURL string, az liquid.AvailabilityZone, projectID liquid.ProjectUUID) liquid.ServiceUsageReport {
	usageReq := liquid.ServiceUsageRequest{AllAZs: []liquid.AvailabilityZone{az}}
	body := must.Return(json.Marshal(usageReq))
	url := fmt.Sprintf("%s/commitments/v1/projects/%s/report-usage", baseURL, projectID)
	httpReq := must.Return(http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body)))
	httpReq.Header.Set("Content-Type", "application/json")

	//nolint:bodyclose
	resp := must.Return(http.DefaultClient.Do(httpReq))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		bodyBytes := must.Return(io.ReadAll(resp.Body))
		panic(fmt.Sprintf("usage API returned %d: %s", resp.StatusCode, bodyBytes))
	}
	var report liquid.ServiceUsageReport
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		panic(fmt.Sprintf("failed to decode ServiceUsageReport: %v", err))
	}
	return report
}

// e2eLogUsageReport logs the usage summary from a ServiceUsageReport.
// For each resource it logs the total usage and, for subresources with attributes,
// counts committed (commitment_id present) vs PAYG (commitment_id absent) instances.
func e2eLogUsageReport(report liquid.ServiceUsageReport, az liquid.AvailabilityZone, projectID liquid.ProjectUUID) {
	for resourceName, resReport := range report.Resources {
		if resReport == nil {
			continue
		}
		azReport := resReport.PerAZ[az]
		if azReport == nil {
			continue
		}
		committed := 0
		payg := 0
		for _, sub := range azReport.Subresources {
			if len(sub.Attributes) == 0 {
				continue
			}
			var attrs map[string]any
			if err := json.Unmarshal(sub.Attributes, &attrs); err == nil && attrs["commitment_id"] != nil {
				committed++
			} else {
				payg++
			}
		}
		slog.Info("usage report",
			"project", projectID, "az", az, "resource", resourceName,
			"usage", azReport.Usage,
			"subresources", len(azReport.Subresources),
			"committed", committed,
			"payg", payg,
		)
	}
}

// e2eSendChangeCommitments sends a change-commitments request.
// Panics on HTTP non-200 (infrastructure error).
// Returns the rejection reason on 200+rejection (expected for capacity-constrained clusters).
// Returns "" on success.
func e2eSendChangeCommitments(ctx context.Context, baseURL string, req liquid.CommitmentChangeRequest) string {
	body := must.Return(json.Marshal(req))
	httpReq := must.Return(http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/commitments/v1/change-commitments", bytes.NewReader(body)))
	httpReq.Header.Set("Content-Type", "application/json")

	//nolint:bodyclose
	resp := must.Return(http.DefaultClient.Do(httpReq))
	defer resp.Body.Close()
	respBody := must.Return(io.ReadAll(resp.Body))

	if resp.StatusCode != http.StatusOK {
		panic(fmt.Sprintf("change-commitments returned %d: %s", resp.StatusCode, respBody))
	}
	var result liquid.CommitmentChangeResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		panic(fmt.Sprintf("failed to decode change-commitments response: %v", err))
	}
	return result.RejectionReason
}

// e2eFetchServiceInfo fetches and decodes /info. Panics on failure.
func e2eFetchServiceInfo(ctx context.Context, baseURL string) liquid.ServiceInfo {
	httpReq := must.Return(http.NewRequestWithContext(ctx, http.MethodGet,
		baseURL+"/commitments/v1/info", http.NoBody))
	httpReq.Header.Set("Accept", "application/json")
	//nolint:bodyclose
	resp := must.Return(http.DefaultClient.Do(httpReq))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		bodyBytes := must.Return(io.ReadAll(resp.Body))
		panic(fmt.Sprintf("info endpoint returned %d: %s", resp.StatusCode, bodyBytes))
	}
	var info liquid.ServiceInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		panic(fmt.Sprintf("failed to decode ServiceInfo: %v", err))
	}
	return info
}

func e2eBaseURL(config E2EChecksConfig) string {
	if config.BaseURL != "" {
		return config.BaseURL
	}
	return defaultCommitmentsAPIURL
}

// RunCommitmentsE2EChecks runs all e2e checks for the commitments API.
func RunCommitmentsE2EChecks(ctx context.Context, config E2EChecksConfig) {
	slog.Info("running commitments e2e checks")
	CheckCommitmentsInfoEndpoint(ctx, config)
	CheckCommitmentsRoundTrip(ctx, config)
	CheckCommitmentsMultiFlavorGroupBatch(ctx, config)
	slog.Info("all commitments e2e checks passed")
}
