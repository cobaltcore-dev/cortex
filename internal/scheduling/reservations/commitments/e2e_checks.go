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

	// defaultE2EProjectUUID is a well-known fake project UUID used when no TestProjectID is configured.
	// It is intentionally not a real OpenStack project — commitments created under it self-expire.
	defaultE2EProjectUUID = "00000000-0000-0000-0000-000000000e2e"
)

// E2EChecksConfig holds the configuration for CR e2e checks.
type E2EChecksConfig struct {
	// BaseURL for the commitments API. If empty, defaults to defaultCommitmentsAPIURL.
	BaseURL string `json:"baseURL"`
	// RoundTripCheck holds optional overrides for the round-trip check.
	// If nil, defaults are used: testProjectID = defaultE2EProjectUUID, az = "".
	RoundTripCheck *E2ERoundTripConfig `json:"roundTripCheck,omitempty"`
}

// E2ERoundTripConfig holds optional overrides for the create→delete round-trip e2e check.
type E2ERoundTripConfig struct {
	// AZ is the availability zone to use (e.g. "qa-de-1d"). Defaults to "" if not set.
	AZ string `json:"az"`
	// TestProjectID is the OpenStack project UUID to create test commitments under.
	// Defaults to defaultE2EProjectUUID if not set.
	TestProjectID string `json:"testProjectID"`
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

// CheckCommitmentsRoundTrip iterates all HandlesCommitments resources from /info and for each one:
//  1. Creates a confirmed test commitment (amount=2, expires in 5 minutes)
//  2. If accepted: calls the usage API to verify it returns 200, then deletes the commitment
//  3. If rejected: logs the reason and continues — capacity rejection is not an error
//
// Panics on infrastructure failures (non-200 from the API, deletion failure after acceptance).
func CheckCommitmentsRoundTrip(ctx context.Context, config E2EChecksConfig) {
	baseURL := e2eBaseURL(config)
	az := liquid.AvailabilityZone("")
	projectID := liquid.ProjectUUID(defaultE2EProjectUUID)
	if rt := config.RoundTripCheck; rt != nil {
		if rt.AZ != "" {
			az = liquid.AvailabilityZone(rt.AZ)
		}
		if rt.TestProjectID != "" {
			projectID = liquid.ProjectUUID(rt.TestProjectID)
		}
	}

	serviceInfo := e2eFetchServiceInfo(ctx, baseURL)

	checked := 0
	for resourceName, resInfo := range serviceInfo.Resources {
		if !resInfo.HandlesCommitments {
			continue
		}
		e2eRoundTripResource(ctx, baseURL, serviceInfo.Version, az, projectID, resourceName)
		checked++
	}

	if checked == 0 {
		slog.Warn("round-trip check: no HandlesCommitments resources found in /info — nothing checked")
	}
}

// e2eRoundTripResource runs the create→usageCheck→delete cycle for one resource.
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

	// Smoke-check the usage API: verifies the usage calculation pipeline works for this project.
	e2eCheckUsageAPI(ctx, baseURL, az, projectID)
}

// e2eCheckUsageAPI calls POST /commitments/v1/projects/:id/report-usage and verifies 200.
// The usage report for a project with no VMs will show zero usage — we only verify the endpoint works.
func e2eCheckUsageAPI(ctx context.Context, baseURL string, az liquid.AvailabilityZone, projectID liquid.ProjectUUID) {
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
	slog.Info("round-trip check: usage API returned 200", "project", projectID)
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
	slog.Info("all commitments e2e checks passed")
}
