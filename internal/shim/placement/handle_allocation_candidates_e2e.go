// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// e2eTestAllocationCandidates tests the /allocation_candidates endpoint.
//
//  1. Pre-cleanup: DELETE leftover RP and custom resource class.
//  2. Create fixtures: PUT a custom resource class, POST a test RP, PUT
//     inventory on the RP (total=100, max_unit=100).
//  3. GET /allocation_candidates?resources=CUSTOM_...:1 — request candidates
//     that can satisfy 1 unit of the custom resource class, then verify:
//     - allocation_requests is non-empty (at least one candidate found).
//     - provider_summaries contains the test RP's UUID.
//  4. Cleanup: DELETE the test RP and custom resource class.
func e2eTestAllocationCandidates(ctx context.Context, _ client.Client) error {
	log := logf.FromContext(ctx)
	log.Info("Running allocation candidates endpoint e2e test")
	config, err := conf.GetConfig[e2eRootConfig]()
	if err != nil {
		log.Error(err, "failed to get e2e config")
		return err
	}
	log.Info("Creating openstack client for allocation candidates e2e test")
	sc, err := makeE2EServiceClient(ctx, config)
	if err != nil {
		log.Error(err, "failed to create placement service client for e2e test")
		return err
	}
	log.Info("Successfully created openstack client for allocation candidates e2e test")

	const testRPUUID = "e2e10000-0000-0000-0000-000000000008"
	const testRPName = "cortex-e2e-test-rp-cand"
	const testRC = "CUSTOM_CORTEX_E2E_CAND_RC"
	const apiVersion = "placement 1.26"

	// Pre-cleanup: delete leftover test resources from a prior run.
	log.Info("Pre-cleanup: deleting leftover test resources")
	for _, cleanup := range []struct {
		method string
		url    string
	}{
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
	log.Info("Creating custom resource class for allocation candidates test", "class", testRC)
	req, err := http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/resource_classes/"+testRC, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create PUT request for resource class", "class", testRC)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	resp, err := sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send PUT request for resource class", "class", testRC)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "PUT /resource_classes returned an error", "class", testRC)
		return err
	}
	log.Info("Successfully created custom resource class", "class", testRC)

	log.Info("Creating test resource provider for allocation candidates test",
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
	req.Header.Set("OpenStack-API-Version", apiVersion)
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
	log.Info("Successfully created test resource provider", "uuid", testRPUUID)

	// Get the generation for the resource provider.
	log.Info("Getting resource provider generation", "uuid", testRPUUID)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers/"+testRPUUID+"/inventories", http.NoBody)
	if err != nil {
		log.Error(err, "failed to create GET request for RP inventories", "uuid", testRPUUID)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send GET request for RP inventories", "uuid", testRPUUID)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "GET RP inventories returned an error", "uuid", testRPUUID)
		return err
	}
	var invResp struct {
		ResourceProviderGeneration int `json:"resource_provider_generation"`
	}
	err = json.NewDecoder(resp.Body).Decode(&invResp)
	if err != nil {
		log.Error(err, "failed to decode RP inventories response", "uuid", testRPUUID)
		return err
	}
	generation := invResp.ResourceProviderGeneration

	// Set inventory on the resource provider.
	log.Info("Setting inventory on test resource provider",
		"uuid", testRPUUID, "class", testRC, "generation", generation)
	putBody, err := json.Marshal(map[string]any{
		"resource_provider_generation": generation,
		"inventories": map[string]any{
			testRC: map[string]any{
				"total":    100,
				"max_unit": 100,
			},
		},
	})
	if err != nil {
		log.Error(err, "failed to marshal request body")
		return err
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/resource_providers/"+testRPUUID+"/inventories",
		bytes.NewReader(putBody))
	if err != nil {
		log.Error(err, "failed to create PUT request for RP inventories", "uuid", testRPUUID)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send PUT request for RP inventories", "uuid", testRPUUID)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "PUT RP inventories returned an error", "uuid", testRPUUID)
		return err
	}
	log.Info("Successfully set inventory on test resource provider",
		"uuid", testRPUUID, "class", testRC)

	// Test GET /allocation_candidates with our custom resource class.
	log.Info("Testing GET /allocation_candidates", "resources", testRC+":1")
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet,
		sc.Endpoint+"/allocation_candidates?resources="+testRC+":1",
		http.NoBody)
	if err != nil {
		log.Error(err, "failed to create GET request for allocation_candidates")
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send GET request to /allocation_candidates")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "GET /allocation_candidates returned an error")
		return err
	}
	var candResp struct {
		AllocationRequests []json.RawMessage          `json:"allocation_requests"`
		ProviderSummaries  map[string]json.RawMessage `json:"provider_summaries"`
	}
	err = json.NewDecoder(resp.Body).Decode(&candResp)
	if err != nil {
		log.Error(err, "failed to decode allocation candidates response")
		return err
	}
	if len(candResp.AllocationRequests) == 0 {
		err := errors.New("expected at least 1 allocation request, got 0")
		log.Error(err, "no allocation candidates returned")
		return err
	}
	if _, ok := candResp.ProviderSummaries[testRPUUID]; !ok {
		err := fmt.Errorf("expected provider %s in summaries", testRPUUID)
		log.Error(err, "test resource provider not found in provider summaries")
		return err
	}
	log.Info("Successfully retrieved allocation candidates",
		"allocationRequests", len(candResp.AllocationRequests),
		"providerSummaries", len(candResp.ProviderSummaries))

	// Cleanup: delete the resource provider and custom resource class.
	log.Info("Cleaning up test resources")
	req, err = http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/resource_providers/"+testRPUUID, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create DELETE request for resource provider", "uuid", testRPUUID)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send DELETE request for resource provider", "uuid", testRPUUID)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "DELETE resource provider returned an error", "uuid", testRPUUID)
		return err
	}
	log.Info("Successfully deleted test resource provider", "uuid", testRPUUID)

	req, err = http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/resource_classes/"+testRC, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create DELETE request for resource class", "class", testRC)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send DELETE request for resource class", "class", testRC)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "DELETE resource class returned an error", "class", testRC)
		return err
	}
	log.Info("Successfully deleted custom resource class", "class", testRC)

	return nil
}

func init() {
	e2eTests = append(e2eTests, e2eTest{name: "allocation_candidates", run: e2eTestAllocationCandidates})
}
