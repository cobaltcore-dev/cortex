// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/gophercloud/gophercloud/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// e2eTestAllocations tests the /allocations/{consumer_uuid} and
// POST /allocations (batch) endpoints.
//
// In passthrough mode: exercises the upstream placement path with a
// dynamically created resource provider and custom resource class.
// In hybrid/crd mode: exercises the CRD-backed booking path using a
// real KVM hypervisor discovered from the cluster.
func e2eTestAllocations(ctx context.Context, cl client.Client) error {
	log := logf.FromContext(ctx)
	log.Info("Running allocations endpoint e2e test")
	config, err := conf.GetConfig[e2eRootConfig]()
	if err != nil {
		log.Error(err, "failed to get e2e config")
		return err
	}
	log.Info("Creating openstack client for allocations e2e test")
	sc, err := makeE2EServiceClient(ctx, config)
	if err != nil {
		log.Error(err, "failed to create placement service client for e2e test")
		return err
	}
	log.Info("Successfully created openstack client for allocations e2e test")

	mode := e2eCurrentMode(ctx)
	switch mode {
	case FeatureModePassthrough:
		return e2ePassthroughAllocations(ctx, sc)
	case FeatureModeHybrid, FeatureModeCRD:
		return e2eCRDAllocations(ctx, sc, cl)
	default:
		return fmt.Errorf("unexpected mode %q", mode)
	}
}

func e2ePassthroughAllocations(ctx context.Context, sc *gophercloud.ServiceClient) error {
	log := logf.FromContext(ctx)

	const testRPUUID = "e2e10000-0000-0000-0000-000000000007"
	const testRPName = "cortex-e2e-test-rp-alloc"
	const testRC = "CUSTOM_CORTEX_E2E_ALLOC_RC"
	const consumerUUID1 = "e2e20000-0000-0000-0000-000000000001"
	const consumerUUID2 = "e2e20000-0000-0000-0000-000000000002"
	const projectID = "e2e40000-0000-0000-0000-000000000001"
	const userID = "e2e50000-0000-0000-0000-000000000001"
	const apiVersion = "placement 1.28"

	// Pre-cleanup: delete allocations, resource provider, and resource class.
	log.Info("Pre-cleanup: deleting leftover test resources")
	for _, cleanup := range []struct {
		method string
		url    string
	}{
		{http.MethodDelete, sc.Endpoint + "/allocations/" + consumerUUID1},
		{http.MethodDelete, sc.Endpoint + "/allocations/" + consumerUUID2},
		{http.MethodDelete, sc.Endpoint + "/resource_providers/" + testRPUUID},
		{http.MethodDelete, sc.Endpoint + "/resource_classes/" + testRC},
	} {
		req, err := http.NewRequestWithContext(ctx, cleanup.method, cleanup.url, http.NoBody)
		if err != nil {
			log.Error(err, "failed to create pre-cleanup request", "url", cleanup.url)
			return err
		}
		req.Header.Set("X-Auth-Token", sc.TokenID)
		req.Header.Set("OpenStack-API-Version", apiVersion)
		resp, err := sc.HTTPClient.Do(req)
		if err != nil {
			log.Error(err, "failed to send pre-cleanup request", "url", cleanup.url)
			return err
		}
		resp.Body.Close()
		log.Info("Pre-cleanup request completed", "url", cleanup.url, "status", resp.StatusCode)
	}

	// Create fixtures: custom resource class, resource provider, and inventory.
	log.Info("Creating custom resource class for allocations test", "class", testRC)
	req, err := http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/resource_classes/"+testRC, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	resp, err := sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("PUT /resource_classes: unexpected status %d", resp.StatusCode)
	}
	log.Info("Successfully created custom resource class", "class", testRC)

	log.Info("Creating test resource provider", "uuid", testRPUUID, "name", testRPName)
	body, err := json.Marshal(map[string]string{"name": testRPName, "uuid": testRPUUID})
	if err != nil {
		return err
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPost, sc.Endpoint+"/resource_providers", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("POST /resource_providers: unexpected status %d", resp.StatusCode)
	}
	log.Info("Successfully created test resource provider", "uuid", testRPUUID)

	// Get the generation for the resource provider.
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers/"+testRPUUID+"/inventories", http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET RP inventories: unexpected status %d", resp.StatusCode)
	}
	var invResp struct {
		ResourceProviderGeneration int `json:"resource_provider_generation"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&invResp); err != nil {
		return err
	}
	generation := invResp.ResourceProviderGeneration

	// Set inventory on the resource provider.
	log.Info("Setting inventory on test resource provider", "total", 100)
	putBody, err := json.Marshal(map[string]any{
		"resource_provider_generation": generation,
		"inventories": map[string]any{
			testRC: map[string]any{"total": 100},
		},
	})
	if err != nil {
		return err
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/resource_providers/"+testRPUUID+"/inventories",
		bytes.NewReader(putBody))
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("PUT RP inventories: unexpected status %d", resp.StatusCode)
	}
	log.Info("Successfully set inventory on test resource provider")

	// Test GET /allocations/{consumer_uuid} (empty).
	log.Info("Testing GET /allocations (empty)", "consumer", consumerUUID1)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/allocations/"+consumerUUID1, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET /allocations (empty): unexpected status %d", resp.StatusCode)
	}
	var allocResp struct {
		Allocations map[string]json.RawMessage `json:"allocations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&allocResp); err != nil {
		return err
	}
	log.Info("Successfully retrieved empty allocations", "count", len(allocResp.Allocations))

	// Test PUT /allocations/{consumer_uuid} (create allocation).
	log.Info("Testing PUT /allocations (create)", "consumer", consumerUUID1)
	allocBody, err := json.Marshal(map[string]any{
		"allocations": map[string]any{
			testRPUUID: map[string]any{"resources": map[string]int{testRC: 10}},
		},
		"project_id":          projectID,
		"user_id":             userID,
		"consumer_generation": nil,
	})
	if err != nil {
		return err
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/allocations/"+consumerUUID1,
		bytes.NewReader(allocBody))
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("PUT /allocations (create): unexpected status %d", resp.StatusCode)
	}
	log.Info("Successfully created allocation", "consumer", consumerUUID1)

	// Test GET /allocations/{consumer_uuid} (after PUT).
	log.Info("Testing GET /allocations (after PUT)", "consumer", consumerUUID1)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/allocations/"+consumerUUID1, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET /allocations (after PUT): unexpected status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&allocResp); err != nil {
		return err
	}
	if _, ok := allocResp.Allocations[testRPUUID]; !ok {
		return fmt.Errorf("expected allocation against RP %s, got keys %v", testRPUUID, allocResp.Allocations)
	}
	log.Info("Verified allocation exists after PUT")

	// Test POST /allocations (batch manage) — create a second consumer.
	log.Info("Testing POST /allocations (batch)", "consumer", consumerUUID2)
	batchBody, err := json.Marshal(map[string]any{
		consumerUUID2: map[string]any{
			"allocations": map[string]any{
				testRPUUID: map[string]any{"resources": map[string]int{testRC: 5}},
			},
			"project_id":          projectID,
			"user_id":             userID,
			"consumer_generation": nil,
		},
	})
	if err != nil {
		return err
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPost, sc.Endpoint+"/allocations",
		bytes.NewReader(batchBody))
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("POST /allocations (batch): unexpected status %d", resp.StatusCode)
	}
	log.Info("Successfully created batch allocation", "consumer", consumerUUID2)

	// Verify the second consumer's allocation.
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/allocations/"+consumerUUID2, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET /allocations (consumer2): unexpected status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&allocResp); err != nil {
		return err
	}
	if _, ok := allocResp.Allocations[testRPUUID]; !ok {
		return fmt.Errorf("expected allocation for consumer2 against RP %s", testRPUUID)
	}
	log.Info("Verified second consumer's allocation")

	// Test DELETE /allocations/{consumer_uuid} for both consumers.
	for _, consumer := range []string{consumerUUID1, consumerUUID2} {
		log.Info("Testing DELETE /allocations", "consumer", consumer)
		req, err = http.NewRequestWithContext(ctx,
			http.MethodDelete, sc.Endpoint+"/allocations/"+consumer, http.NoBody)
		if err != nil {
			return err
		}
		req.Header.Set("X-Auth-Token", sc.TokenID)
		req.Header.Set("OpenStack-API-Version", apiVersion)
		resp, err = sc.HTTPClient.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("DELETE /allocations: unexpected status %d for consumer %s", resp.StatusCode, consumer)
		}
		log.Info("Successfully deleted allocation", "consumer", consumer)
	}

	// Cleanup: delete the resource provider and custom resource class.
	log.Info("Cleaning up test resources")
	req, err = http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/resource_providers/"+testRPUUID, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()

	req, err = http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/resource_classes/"+testRC, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	log.Info("Cleanup complete")

	return nil
}

// e2eCRDAllocations tests the CRD/hybrid path by discovering a real KVM
// hypervisor in the cluster, writing a booking to it, and then exercising
// GET/PUT/DELETE/POST through the shim's allocation handlers.
func e2eCRDAllocations(ctx context.Context, sc *gophercloud.ServiceClient, cl client.Client) error {
	log := logf.FromContext(ctx)

	const consumerUUID = "e2e20000-0000-0000-0000-000000000010"
	const consumerUUID2 = "e2e20000-0000-0000-0000-000000000011"
	const projectID = "e2e40000-0000-0000-0000-000000000001"
	const userID = "e2e50000-0000-0000-0000-000000000001"
	const apiVersion = "placement 1.28"

	// Discover a KVM hypervisor with a non-empty OpenStack ID.
	var hvs hv1.HypervisorList
	if err := cl.List(ctx, &hvs); err != nil {
		return fmt.Errorf("failed to list hypervisors: %w", err)
	}
	var kvmHV *hv1.Hypervisor
	for i := range hvs.Items {
		if hvs.Items[i].Status.HypervisorID != "" {
			kvmHV = &hvs.Items[i]
			break
		}
	}
	if kvmHV == nil {
		log.Info("No KVM hypervisors with OpenStack ID found, skipping CRD allocations tests")
		return nil
	}
	kvmUUID := kvmHV.Status.HypervisorID
	log.Info("Using KVM hypervisor for CRD allocations e2e", "uuid", kvmUUID, "name", kvmHV.Name)

	// Save original bookings for restoration.
	originalBookings := kvmHV.Spec.Bookings

	// Always restore original bookings on exit.
	defer func() {
		log.Info("Restoring original bookings", "name", kvmHV.Name)
		for range 5 {
			if err := cl.Get(ctx, client.ObjectKeyFromObject(kvmHV), kvmHV); err != nil {
				log.Error(err, "failed to refetch hypervisor for restoration")
				return
			}
			kvmHV.Spec.Bookings = originalBookings
			if err := cl.Update(ctx, kvmHV); err != nil {
				if apierrors.IsConflict(err) {
					continue
				}
				log.Error(err, "failed to restore original bookings")
				return
			}
			return
		}
		log.Error(nil, "exhausted retries restoring original bookings")
	}()

	// Pre-cleanup: remove any leftover test bookings from prior runs.
	kvmHV.Spec.Bookings = removeTestBookings(kvmHV.Spec.Bookings, consumerUUID, consumerUUID2)
	if err := cl.Update(ctx, kvmHV); err != nil {
		return fmt.Errorf("pre-cleanup: failed to remove leftover bookings: %w", err)
	}
	if err := cl.Get(ctx, client.ObjectKeyFromObject(kvmHV), kvmHV); err != nil {
		return fmt.Errorf("failed to refetch hypervisor after pre-cleanup: %w", err)
	}

	// 1. Test GET /allocations/{consumer_uuid} — empty (consumer not booked).
	log.Info("Testing GET /allocations (empty, CRD)", "consumer", consumerUUID)
	if err := e2ePollUntil(ctx, 10*time.Second, func() (bool, error) {
		req, err := http.NewRequestWithContext(ctx,
			http.MethodGet, sc.Endpoint+"/allocations/"+consumerUUID, http.NoBody)
		if err != nil {
			return false, err
		}
		req.Header.Set("X-Auth-Token", sc.TokenID)
		req.Header.Set("OpenStack-API-Version", apiVersion)
		req.Header.Set("Accept", "application/json")
		resp, err := sc.HTTPClient.Do(req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Errorf("GET /allocations (empty): expected 200, got %d", resp.StatusCode)
		}
		var r allocationsResponse
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
			return false, err
		}
		return len(r.Allocations) == 0, nil
	}); err != nil {
		return fmt.Errorf("GET empty allocations: %w", err)
	}
	log.Info("Verified empty allocations for unbooked consumer")

	// 2. Test PUT /allocations/{consumer_uuid} — create allocation (new consumer).
	log.Info("Testing PUT /allocations (create, CRD)", "consumer", consumerUUID, "rp", kvmUUID)
	allocBody, err := json.Marshal(map[string]any{
		"allocations": map[string]any{
			kvmUUID: map[string]any{"resources": map[string]int64{"VCPU": 2, "MEMORY_MB": 4096}},
		},
		"project_id":          projectID,
		"user_id":             userID,
		"consumer_generation": nil,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/allocations/"+consumerUUID,
		bytes.NewReader(allocBody))
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("PUT /allocations (create): expected 204, got %d", resp.StatusCode)
	}
	log.Info("Successfully created allocation via PUT (CRD)")

	// 3. Test GET /allocations/{consumer_uuid} — verify booking present.
	log.Info("Testing GET /allocations (after PUT, CRD)", "consumer", consumerUUID)
	var getResp allocationsResponse
	if err := e2ePollUntil(ctx, 10*time.Second, func() (bool, error) {
		req, err := http.NewRequestWithContext(ctx,
			http.MethodGet, sc.Endpoint+"/allocations/"+consumerUUID, http.NoBody)
		if err != nil {
			return false, err
		}
		req.Header.Set("X-Auth-Token", sc.TokenID)
		req.Header.Set("OpenStack-API-Version", apiVersion)
		req.Header.Set("Accept", "application/json")
		resp, err := sc.HTTPClient.Do(req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Errorf("GET /allocations (after PUT): expected 200, got %d", resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(&getResp); err != nil {
			return false, err
		}
		_, ok := getResp.Allocations[kvmUUID]
		return ok, nil
	}); err != nil {
		return fmt.Errorf("GET allocations after PUT: %w (resp: %+v)", err, getResp)
	}
	if getResp.Allocations[kvmUUID].Resources["VCPU"] != 2 {
		return fmt.Errorf("VCPU = %d, want 2", getResp.Allocations[kvmUUID].Resources["VCPU"])
	}
	if getResp.Allocations[kvmUUID].Resources["MEMORY_MB"] != 4096 {
		return fmt.Errorf("MEMORY_MB = %d, want 4096", getResp.Allocations[kvmUUID].Resources["MEMORY_MB"])
	}
	log.Info("Verified allocation present after PUT (CRD)")

	// 4. Test PUT with wrong consumer_generation — should 409.
	log.Info("Testing PUT /allocations (stale generation, CRD)")
	staleBody, err := json.Marshal(map[string]any{
		"allocations": map[string]any{
			kvmUUID: map[string]any{"resources": map[string]int64{"VCPU": 8}},
		},
		"project_id":          projectID,
		"user_id":             userID,
		"consumer_generation": 999,
	})
	if err != nil {
		return err
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/allocations/"+consumerUUID,
		bytes.NewReader(staleBody))
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		return fmt.Errorf("PUT /allocations (stale gen): expected 409, got %d", resp.StatusCode)
	}
	log.Info("Verified generation conflict returns 409 (CRD)")

	// 5. Test POST /allocations (batch) — create a second consumer.
	log.Info("Testing POST /allocations (batch, CRD)", "consumer", consumerUUID2)
	batchBody, err := json.Marshal(map[string]any{
		consumerUUID2: map[string]any{
			"allocations": map[string]any{
				kvmUUID: map[string]any{"resources": map[string]int64{"VCPU": 1, "MEMORY_MB": 2048}},
			},
			"project_id":          projectID,
			"user_id":             userID,
			"consumer_generation": nil,
		},
	})
	if err != nil {
		return err
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPost, sc.Endpoint+"/allocations",
		bytes.NewReader(batchBody))
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("POST /allocations (batch): expected 204, got %d", resp.StatusCode)
	}
	log.Info("Successfully created batch allocation (CRD)")

	// Verify second consumer's allocation.
	var getResp2 allocationsResponse
	if err := e2ePollUntil(ctx, 10*time.Second, func() (bool, error) {
		req, err := http.NewRequestWithContext(ctx,
			http.MethodGet, sc.Endpoint+"/allocations/"+consumerUUID2, http.NoBody)
		if err != nil {
			return false, err
		}
		req.Header.Set("X-Auth-Token", sc.TokenID)
		req.Header.Set("OpenStack-API-Version", apiVersion)
		req.Header.Set("Accept", "application/json")
		resp, err := sc.HTTPClient.Do(req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Errorf("GET /allocations (consumer2): expected 200, got %d", resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(&getResp2); err != nil {
			return false, err
		}
		_, ok := getResp2.Allocations[kvmUUID]
		return ok, nil
	}); err != nil {
		return fmt.Errorf("GET allocations (consumer2): %w", err)
	}
	log.Info("Verified second consumer's allocation (CRD)")

	// 6. Test DELETE /allocations/{consumer_uuid}.
	for _, consumer := range []string{consumerUUID, consumerUUID2} {
		log.Info("Testing DELETE /allocations (CRD)", "consumer", consumer)
		req, err = http.NewRequestWithContext(ctx,
			http.MethodDelete, sc.Endpoint+"/allocations/"+consumer, http.NoBody)
		if err != nil {
			return err
		}
		req.Header.Set("X-Auth-Token", sc.TokenID)
		req.Header.Set("OpenStack-API-Version", apiVersion)
		resp, err = sc.HTTPClient.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			return fmt.Errorf("DELETE /allocations: expected 204, got %d for consumer %s", resp.StatusCode, consumer)
		}
		log.Info("Successfully deleted allocation (CRD)", "consumer", consumer)
	}

	// 7. Verify GET after DELETE returns empty.
	if err := e2ePollUntil(ctx, 10*time.Second, func() (bool, error) {
		req, err := http.NewRequestWithContext(ctx,
			http.MethodGet, sc.Endpoint+"/allocations/"+consumerUUID, http.NoBody)
		if err != nil {
			return false, err
		}
		req.Header.Set("X-Auth-Token", sc.TokenID)
		req.Header.Set("OpenStack-API-Version", apiVersion)
		req.Header.Set("Accept", "application/json")
		resp, err := sc.HTTPClient.Do(req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Errorf("GET /allocations (post-delete): expected 200, got %d", resp.StatusCode)
		}
		var r allocationsResponse
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
			return false, err
		}
		return len(r.Allocations) == 0, nil
	}); err != nil {
		return fmt.Errorf("GET allocations after DELETE: %w", err)
	}
	log.Info("Verified allocations empty after DELETE (CRD)")

	// 8. Test DELETE /allocations for unknown consumer — should 404.
	unknownConsumer := "e2e20000-0000-0000-0000-ffffffffffff"
	req, err = http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/allocations/"+unknownConsumer, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("DELETE /allocations (unknown): expected 404, got %d", resp.StatusCode)
	}
	log.Info("Verified DELETE for unknown consumer returns 404 (CRD)")

	return nil
}

// removeTestBookings removes consumer bookings matching any of the given UUIDs.
func removeTestBookings(bookings []hv1.Booking, uuids ...string) []hv1.Booking {
	uuidSet := make(map[string]bool, len(uuids))
	for _, u := range uuids {
		uuidSet[u] = true
	}
	var kept []hv1.Booking
	for i := range bookings {
		if bookings[i].Consumer != nil && uuidSet[bookings[i].Consumer.UUID] {
			continue
		}
		kept = append(kept, bookings[i])
	}
	return kept
}

func init() {
	e2eTests = append(e2eTests, e2eTest{name: "allocations", run: e2eWrapWithModes(e2eTestAllocations)})
}
