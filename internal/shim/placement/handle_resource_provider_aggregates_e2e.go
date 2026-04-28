// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/gophercloud/gophercloud/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// e2eTestResourceProviderAggregates tests the
// /resource_providers/{uuid}/aggregates endpoints.
//
// In passthrough mode: exercises the upstream placement path with a
// dynamically created resource provider.
// In hybrid/crd mode: exercises the spec.groups-backed CRD path using a
// real KVM hypervisor discovered from the cluster.
func e2eTestResourceProviderAggregates(ctx context.Context, cl client.Client) error {
	log := logf.FromContext(ctx)
	log.Info("Running resource provider aggregates endpoint e2e test")
	config, err := conf.GetConfig[e2eRootConfig]()
	if err != nil {
		log.Error(err, "failed to get e2e config")
		return err
	}
	log.Info("Creating openstack client for resource provider aggregates e2e test")
	sc, err := makeE2EServiceClient(ctx, config)
	if err != nil {
		log.Error(err, "failed to create placement service client for e2e test")
		return err
	}
	log.Info("Successfully created openstack client for resource provider aggregates e2e test")

	mode := e2eCurrentMode(ctx)
	switch mode {
	case FeatureModePassthrough:
		return e2ePassthroughResourceProviderAggregates(ctx, sc)
	case FeatureModeHybrid, FeatureModeCRD:
		return e2eCRDResourceProviderAggregates(ctx, sc, cl)
	default:
		return fmt.Errorf("unexpected mode %q", mode)
	}
}

func e2ePassthroughResourceProviderAggregates(ctx context.Context, sc *gophercloud.ServiceClient) error {
	log := logf.FromContext(ctx)

	const testRPUUID = "e2e10000-0000-0000-0000-000000000004"
	const testRPName = "cortex-e2e-test-rp-agg"
	const testAggUUID1 = "e2e30000-0000-0000-0000-000000000001"
	const testAggUUID2 = "e2e30000-0000-0000-0000-000000000002"

	// Pre-cleanup: delete leftover test RP.
	log.Info("Pre-cleanup: deleting leftover test resource provider", "uuid", testRPUUID)
	req, err := http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/resource_providers/"+testRPUUID, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create pre-cleanup request")
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.19")
	resp, err := sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send pre-cleanup request")
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound &&
		(resp.StatusCode < 200 || resp.StatusCode >= 300) {
		err := fmt.Errorf("unexpected status code during pre-cleanup: %d", resp.StatusCode)
		log.Error(err, "pre-cleanup failed")
		return err
	}
	log.Info("Pre-cleanup completed", "status", resp.StatusCode)

	// Create a test resource provider.
	log.Info("Creating test resource provider for aggregates test",
		"uuid", testRPUUID, "name", testRPName)
	body, err := json.Marshal(map[string]string{
		"name": testRPName,
		"uuid": testRPUUID,
	})
	if err != nil {
		log.Error(err, "failed to marshal request body")
		return err
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPost, sc.Endpoint+"/resource_providers", bytes.NewReader(body))
	if err != nil {
		log.Error(err, "failed to create POST request for resource_providers")
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.20")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send POST request to /resource_providers")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "POST /resource_providers returned an error")
		return err
	}
	log.Info("Successfully created test resource provider for aggregates test",
		"uuid", testRPUUID)

	// Deferred cleanup.
	defer func() {
		log.Info("Deferred cleanup: deleting test resource provider", "uuid", testRPUUID)
		dReq, dErr := http.NewRequestWithContext(ctx,
			http.MethodDelete, sc.Endpoint+"/resource_providers/"+testRPUUID, http.NoBody)
		if dErr != nil {
			log.Error(dErr, "deferred cleanup: failed to create DELETE request")
			return
		}
		dReq.Header.Set("X-Auth-Token", sc.TokenID)
		dReq.Header.Set("OpenStack-API-Version", "placement 1.19")
		dResp, dErr := sc.HTTPClient.Do(dReq)
		if dErr != nil {
			log.Error(dErr, "deferred cleanup: failed to send DELETE request")
			return
		}
		dResp.Body.Close()
		log.Info("Deferred cleanup completed", "status", dResp.StatusCode)
	}()

	// Test GET (empty).
	log.Info("Testing GET /resource_providers/{uuid}/aggregates (empty)", "uuid", testRPUUID)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers/"+testRPUUID+"/aggregates", http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.19")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET RP aggregates: unexpected status %d", resp.StatusCode)
	}
	var aggResp struct {
		Aggregates                 []string `json:"aggregates"`
		ResourceProviderGeneration int      `json:"resource_provider_generation"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&aggResp); err != nil {
		return err
	}
	if len(aggResp.Aggregates) != 0 {
		return fmt.Errorf("expected 0 initial aggregates, got %d", len(aggResp.Aggregates))
	}
	log.Info("Verified empty aggregates", "generation", aggResp.ResourceProviderGeneration)

	// Test PUT (associate aggregates).
	putBody, err := json.Marshal(map[string]any{
		"resource_provider_generation": aggResp.ResourceProviderGeneration,
		"aggregates":                   []string{testAggUUID1, testAggUUID2},
	})
	if err != nil {
		return err
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/resource_providers/"+testRPUUID+"/aggregates",
		bytes.NewReader(putBody))
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.19")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("PUT RP aggregates: unexpected status %d", resp.StatusCode)
	}
	log.Info("Successfully associated aggregates")

	// Test GET (after PUT).
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers/"+testRPUUID+"/aggregates", http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.19")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&aggResp); err != nil {
		return err
	}
	if !slices.Contains(aggResp.Aggregates, testAggUUID1) || !slices.Contains(aggResp.Aggregates, testAggUUID2) {
		return fmt.Errorf("expected aggregates %v and %v, got %v", testAggUUID1, testAggUUID2, aggResp.Aggregates)
	}
	log.Info("Verified aggregates present after PUT")

	// Clear aggregates by PUT with empty list.
	putBody, err = json.Marshal(map[string]any{
		"resource_provider_generation": aggResp.ResourceProviderGeneration,
		"aggregates":                   []string{},
	})
	if err != nil {
		return err
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/resource_providers/"+testRPUUID+"/aggregates",
		bytes.NewReader(putBody))
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.19")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("PUT RP aggregates (clear): unexpected status %d", resp.StatusCode)
	}
	log.Info("Successfully cleared aggregates")

	// Verify empty after clear.
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers/"+testRPUUID+"/aggregates", http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.19")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&aggResp); err != nil {
		return err
	}
	if len(aggResp.Aggregates) != 0 {
		return fmt.Errorf("expected 0 aggregates after clear, got %d", len(aggResp.Aggregates))
	}
	log.Info("Verified aggregates empty after clear")

	// Cleanup.
	req, err = http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/resource_providers/"+testRPUUID, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.19")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()

	return nil
}

// e2eCRDResourceProviderAggregates tests the CRD/hybrid path by discovering a
// real KVM hypervisor in the cluster, seeding spec.groups, and exercising
// GET/PUT through the shim.
func e2eCRDResourceProviderAggregates(ctx context.Context, sc *gophercloud.ServiceClient, cl client.Client) error {
	log := logf.FromContext(ctx)

	// Discover a KVM hypervisor with a non-empty OpenStack ID.
	var hvs hv1.HypervisorList
	if err := cl.List(ctx, &hvs); err != nil {
		log.Error(err, "failed to list hypervisors for CRD aggregates path")
		return err
	}
	var kvmHV *hv1.Hypervisor
	for i := range hvs.Items {
		if hvs.Items[i].Status.HypervisorID != "" {
			kvmHV = &hvs.Items[i]
			break
		}
	}
	if kvmHV == nil {
		log.Info("No KVM hypervisors with OpenStack ID found, skipping CRD aggregates tests")
		return nil
	}
	kvmUUID := kvmHV.Status.HypervisorID
	log.Info("Using KVM hypervisor for CRD aggregates e2e tests", "uuid", kvmUUID, "name", kvmHV.Name)

	// Save original groups for restoration.
	originalGroups := kvmHV.Spec.Groups

	// Seed spec.groups with test aggregates (preserve non-aggregate groups).
	const testAgg1UUID = "e2e40000-0000-0000-0000-000000000001"
	const testAgg2UUID = "e2e40000-0000-0000-0000-000000000002"
	var nonAggGroups []hv1.Group
	for i := range kvmHV.Spec.Groups {
		if kvmHV.Spec.Groups[i].Aggregate == nil {
			nonAggGroups = append(nonAggGroups, kvmHV.Spec.Groups[i])
		}
	}
	nonAggGroups = append(nonAggGroups,
		hv1.Group{Aggregate: &hv1.AggregateGroup{Name: testAgg1UUID, UUID: testAgg1UUID}},
		hv1.Group{Aggregate: &hv1.AggregateGroup{Name: testAgg2UUID, UUID: testAgg2UUID}},
	)
	kvmHV.Spec.Groups = nonAggGroups
	if err := cl.Update(ctx, kvmHV); err != nil {
		return fmt.Errorf("failed to seed spec.groups with test aggregates: %w", err)
	}
	log.Info("Seeded spec.groups with test aggregates", "uuid", kvmUUID)

	// Always restore original groups on exit.
	defer func() {
		log.Info("Restoring original spec.groups", "uuid", kvmUUID)
		if err := cl.Get(ctx, client.ObjectKeyFromObject(kvmHV), kvmHV); err != nil {
			log.Error(err, "failed to refetch hypervisor for restoration")
			return
		}
		kvmHV.Spec.Groups = originalGroups
		if err := cl.Update(ctx, kvmHV); err != nil {
			log.Error(err, "failed to restore original spec.groups")
		}
	}()

	// Refetch to get updated generation.
	if err := cl.Get(ctx, client.ObjectKeyFromObject(kvmHV), kvmHV); err != nil {
		return fmt.Errorf("failed to refetch hypervisor after seed: %w", err)
	}

	// Test GET — should return the seeded aggregates.
	// Poll because the shim's informer cache may take a moment to observe the update.
	log.Info("Testing GET /resource_providers/{uuid}/aggregates (CRD)", "uuid", kvmUUID)
	var aggResp struct {
		Aggregates                 []string `json:"aggregates"`
		ResourceProviderGeneration int64    `json:"resource_provider_generation"`
	}
	if err := e2ePollUntil(ctx, 10*time.Second, func() (bool, error) {
		req, err := http.NewRequestWithContext(ctx,
			http.MethodGet, sc.Endpoint+"/resource_providers/"+kvmUUID+"/aggregates", http.NoBody)
		if err != nil {
			return false, err
		}
		req.Header.Set("X-Auth-Token", sc.TokenID)
		req.Header.Set("OpenStack-API-Version", "placement 1.19")
		req.Header.Set("Accept", "application/json")
		resp, err := sc.HTTPClient.Do(req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Errorf("GET CRD aggregates: expected 200, got %d", resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(&aggResp); err != nil {
			return false, fmt.Errorf("failed to decode CRD aggregates response: %w", err)
		}
		return slices.Contains(aggResp.Aggregates, testAgg1UUID) &&
			slices.Contains(aggResp.Aggregates, testAgg2UUID), nil
	}); err != nil {
		return fmt.Errorf("waiting for seeded aggregates: %w (got %v)", err, aggResp.Aggregates)
	}
	log.Info("Verified GET returns seeded aggregates from CRD",
		"aggregates", aggResp.Aggregates, "generation", aggResp.ResourceProviderGeneration)

	// Test PUT — replace aggregates.
	const replacementAggUUID = "e2e40000-0000-0000-0000-000000000099"
	putBody, err := json.Marshal(map[string]any{
		"resource_provider_generation": aggResp.ResourceProviderGeneration,
		"aggregates":                   []string{replacementAggUUID},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/resource_providers/"+kvmUUID+"/aggregates",
		bytes.NewReader(putBody))
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.19")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("PUT CRD aggregates: expected 200, got %d", resp.StatusCode)
	}
	log.Info("Successfully replaced aggregates via PUT (CRD)")

	// Test PUT with stale generation — should return 409.
	putBody, err = json.Marshal(map[string]any{
		"resource_provider_generation": aggResp.ResourceProviderGeneration,
		"aggregates":                   []string{"stale-uuid"},
	})
	if err != nil {
		return err
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/resource_providers/"+kvmUUID+"/aggregates",
		bytes.NewReader(putBody))
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.19")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		return fmt.Errorf("PUT CRD aggregates (stale gen): expected 409, got %d", resp.StatusCode)
	}
	log.Info("Verified generation conflict returns 409")

	// Test GET — verify replacement persisted.
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers/"+kvmUUID+"/aggregates", http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.19")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&aggResp); err != nil {
		return err
	}
	if len(aggResp.Aggregates) != 1 || aggResp.Aggregates[0] != replacementAggUUID {
		return fmt.Errorf("expected [%s], got %v", replacementAggUUID, aggResp.Aggregates)
	}
	log.Info("Verified replacement aggregate persisted")

	return nil
}

func init() {
	e2eTests = append(e2eTests, e2eTest{name: "resource_provider_aggregates", run: e2eWrapWithModes(e2eTestResourceProviderAggregates)})
}
