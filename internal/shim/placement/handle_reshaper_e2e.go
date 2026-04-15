// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// e2eTestReshaper tests the POST /reshaper endpoint by atomically moving
// inventory and allocations from one resource provider to another.
//
//  1. Pre-cleanup: DELETE leftover allocation, both RPs, and custom RC.
//  2. Create fixtures: PUT a custom resource class, POST two resource
//     providers (RP-A and RP-B), PUT inventory on RP-A (total=100), PUT
//     an allocation of 10 units on RP-A.
//  3. Gather state: GET consumer_generation from the allocation, GET
//     resource_provider_generation for both RP-A and RP-B.
//  4. POST /reshaper — atomically clear RP-A's inventory, set inventory on
//     RP-B, and re-point the consumer's allocation from RP-A to RP-B.
//  5. Verify RP-A: GET inventories — expect empty.
//  6. Verify RP-B: GET inventories — expect the custom resource class present.
//  7. Verify allocation: GET /allocations/{consumer} — expect it to reference
//     RP-B (not RP-A).
//  8. Cleanup: DELETE allocation, both RPs, and custom resource class.
func e2eTestReshaper(ctx context.Context) error {
	log := logf.FromContext(ctx)
	log.Info("Running reshaper endpoint e2e test")
	config, err := conf.GetConfig[e2eRootConfig]()
	if err != nil {
		log.Error(err, "failed to get e2e config")
		return err
	}
	log.Info("Creating openstack client for reshaper e2e test")
	sc, err := makeE2EServiceClient(ctx, config)
	if err != nil {
		log.Error(err, "failed to create placement service client for e2e test")
		return err
	}
	log.Info("Successfully created openstack client for reshaper e2e test")

	const rpAUUID = "e2e10000-0000-0000-0000-000000000009"
	const rpBUUID = "e2e10000-0000-0000-0000-00000000000a"
	const rpAName = "cortex-e2e-test-rp-resh-a"
	const rpBName = "cortex-e2e-test-rp-resh-b"
	const testRC = "CUSTOM_CORTEX_E2E_RESH_RC"
	const consumerUUID = "e2e20000-0000-0000-0000-000000000003"
	const projectID = "e2e40000-0000-0000-0000-000000000001"
	const userID = "e2e50000-0000-0000-0000-000000000001"
	const apiVersion = "placement 1.30"

	// Pre-cleanup: delete allocation, both RPs, and custom resource class.
	log.Info("Pre-cleanup: deleting leftover test resources")
	for _, cleanup := range []struct {
		method string
		url    string
	}{
		{http.MethodDelete, sc.Endpoint + "/allocations/" + consumerUUID},
		{http.MethodDelete, sc.Endpoint + "/resource_providers/" + rpAUUID},
		{http.MethodDelete, sc.Endpoint + "/resource_providers/" + rpBUUID},
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
		defer resp.Body.Close()
		log.Info("Pre-cleanup request completed", "url", cleanup.url, "status", resp.StatusCode)
	}

	// Helper to get a resource provider's generation.
	getGeneration := func(rpUUID string) (int, error) {
		req, err := http.NewRequestWithContext(ctx,
			http.MethodGet,
			sc.Endpoint+"/resource_providers/"+rpUUID+"/inventories",
			http.NoBody)
		if err != nil {
			return 0, err
		}
		req.Header.Set("X-Auth-Token", sc.TokenID)
		req.Header.Set("OpenStack-API-Version", apiVersion)
		req.Header.Set("Accept", "application/json")
		resp, err := sc.HTTPClient.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return 0, fmt.Errorf("GET inventories for %s: status %d", rpUUID, resp.StatusCode)
		}
		var r struct {
			ResourceProviderGeneration int `json:"resource_provider_generation"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
			return 0, err
		}
		return r.ResourceProviderGeneration, nil
	}

	// Create fixtures: custom resource class and two resource providers.
	log.Info("Creating custom resource class for reshaper test", "class", testRC)
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

	for _, rp := range []struct {
		uuid string
		name string
	}{
		{rpAUUID, rpAName},
		{rpBUUID, rpBName},
	} {
		log.Info("Creating resource provider for reshaper test",
			"uuid", rp.uuid, "name", rp.name)
		body, err := json.Marshal(map[string]string{
			"name": rp.name,
			"uuid": rp.uuid,
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
			log.Error(err, "POST /resource_providers returned an error", "uuid", rp.uuid)
			return err
		}
		log.Info("Successfully created resource provider", "uuid", rp.uuid)
	}

	// Set inventory on RP-A.
	genA, err := getGeneration(rpAUUID)
	if err != nil {
		log.Error(err, "failed to get generation for RP-A", "uuid", rpAUUID)
		return err
	}
	log.Info("Setting inventory on RP-A", "uuid", rpAUUID, "class", testRC,
		"generation", genA)
	putBody, err := json.Marshal(map[string]any{
		"resource_provider_generation": genA,
		"inventories": map[string]any{
			testRC: map[string]any{"total": 100, "max_unit": 100},
		},
	})
	if err != nil {
		log.Error(err, "failed to marshal request body")
		return err
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/resource_providers/"+rpAUUID+"/inventories",
		bytes.NewReader(putBody))
	if err != nil {
		log.Error(err, "failed to create PUT request for RP-A inventories")
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send PUT request for RP-A inventories")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "PUT RP-A inventories returned an error")
		return err
	}
	log.Info("Successfully set inventory on RP-A", "uuid", rpAUUID)

	// Create an allocation on RP-A.
	log.Info("Creating allocation on RP-A", "consumer", consumerUUID,
		"rp", rpAUUID, "amount", 10)
	allocBody, err := json.Marshal(map[string]any{
		"allocations": map[string]any{
			rpAUUID: map[string]any{
				"resources": map[string]int{testRC: 10},
			},
		},
		"project_id":          projectID,
		"user_id":             userID,
		"consumer_generation": nil,
	})
	if err != nil {
		log.Error(err, "failed to marshal request body")
		return err
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/allocations/"+consumerUUID,
		bytes.NewReader(allocBody))
	if err != nil {
		log.Error(err, "failed to create PUT request for allocations")
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send PUT request for allocations")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "PUT /allocations returned an error")
		return err
	}
	log.Info("Successfully created allocation on RP-A", "consumer", consumerUUID)

	// Get consumer_generation.
	log.Info("Getting consumer generation", "consumer", consumerUUID)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/allocations/"+consumerUUID, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create GET request for allocations")
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send GET request for allocations")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "GET /allocations returned an error")
		return err
	}
	var consumerResp struct {
		ConsumerGeneration int `json:"consumer_generation"`
	}
	err = json.NewDecoder(resp.Body).Decode(&consumerResp)
	if err != nil {
		log.Error(err, "failed to decode consumer allocations response")
		return err
	}
	consumerGen := consumerResp.ConsumerGeneration
	log.Info("Got consumer generation", "consumer", consumerUUID,
		"generation", consumerGen)

	// Refresh both RP generations (may have changed due to allocation).
	genA, err = getGeneration(rpAUUID)
	if err != nil {
		log.Error(err, "failed to get updated generation for RP-A")
		return err
	}
	genB, err := getGeneration(rpBUUID)
	if err != nil {
		log.Error(err, "failed to get generation for RP-B")
		return err
	}
	log.Info("Got resource provider generations", "genA", genA, "genB", genB)

	// Test POST /reshaper: move inventory from RP-A to RP-B and re-point
	// the allocation from RP-A to RP-B.
	log.Info("Testing POST /reshaper to move inventory and allocation from RP-A to RP-B")
	reshaperBody, err := json.Marshal(map[string]any{
		"inventories": map[string]any{
			rpAUUID: map[string]any{
				"inventories":                  map[string]any{},
				"resource_provider_generation": genA,
			},
			rpBUUID: map[string]any{
				"inventories": map[string]any{
					testRC: map[string]any{"total": 100, "max_unit": 100},
				},
				"resource_provider_generation": genB,
			},
		},
		"allocations": map[string]any{
			consumerUUID: map[string]any{
				"allocations": map[string]any{
					rpBUUID: map[string]any{
						"resources": map[string]int{testRC: 10},
					},
				},
				"project_id":          projectID,
				"user_id":             userID,
				"consumer_generation": consumerGen,
			},
		},
	})
	if err != nil {
		log.Error(err, "failed to marshal request body")
		return err
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPost, sc.Endpoint+"/reshaper",
		bytes.NewReader(reshaperBody))
	if err != nil {
		log.Error(err, "failed to create POST request for reshaper")
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send POST request for reshaper")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "POST /reshaper returned an error")
		return err
	}
	log.Info("Successfully executed reshaper")

	// Verify: RP-A inventories should be empty.
	log.Info("Verifying RP-A inventories are empty after reshaper", "uuid", rpAUUID)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers/"+rpAUUID+"/inventories", http.NoBody)
	if err != nil {
		log.Error(err, "failed to create GET request for RP-A inventories")
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send GET request for RP-A inventories")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "GET RP-A inventories returned an error")
		return err
	}
	var invResp struct {
		Inventories map[string]json.RawMessage `json:"inventories"`
	}
	err = json.NewDecoder(resp.Body).Decode(&invResp)
	if err != nil {
		log.Error(err, "failed to decode RP-A inventories response")
		return err
	}
	if len(invResp.Inventories) != 0 {
		err := fmt.Errorf("expected 0 inventories on RP-A, got %d", len(invResp.Inventories))
		log.Error(err, "RP-A inventories not empty after reshaper")
		return err
	}
	log.Info("Verified RP-A inventories are empty", "uuid", rpAUUID)

	// Verify: RP-B inventories should have our resource class.
	log.Info("Verifying RP-B inventories after reshaper", "uuid", rpBUUID)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers/"+rpBUUID+"/inventories", http.NoBody)
	if err != nil {
		log.Error(err, "failed to create GET request for RP-B inventories")
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send GET request for RP-B inventories")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "GET RP-B inventories returned an error")
		return err
	}
	err = json.NewDecoder(resp.Body).Decode(&invResp)
	if err != nil {
		log.Error(err, "failed to decode RP-B inventories response")
		return err
	}
	if _, ok := invResp.Inventories[testRC]; !ok {
		err := fmt.Errorf("expected %s inventory on RP-B", testRC)
		log.Error(err, "RP-B missing expected inventory after reshaper")
		return err
	}
	log.Info("Verified RP-B has inventory after reshaper",
		"uuid", rpBUUID, "class", testRC)

	// Verify: allocation should now point to RP-B.
	log.Info("Verifying allocation points to RP-B after reshaper",
		"consumer", consumerUUID)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/allocations/"+consumerUUID, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create GET request for allocations")
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send GET request for allocations")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "GET /allocations returned an error")
		return err
	}
	var allocResp struct {
		Allocations map[string]json.RawMessage `json:"allocations"`
	}
	err = json.NewDecoder(resp.Body).Decode(&allocResp)
	if err != nil {
		log.Error(err, "failed to decode allocations response")
		return err
	}
	if _, ok := allocResp.Allocations[rpBUUID]; !ok {
		err := fmt.Errorf("expected allocation against RP-B %s, got keys %v",
			rpBUUID, func() []string {
				keys := make([]string, 0, len(allocResp.Allocations))
				for k := range allocResp.Allocations {
					keys = append(keys, k)
				}
				return keys
			}())
		log.Error(err, "allocation not pointing to RP-B after reshaper")
		return err
	}
	if _, ok := allocResp.Allocations[rpAUUID]; ok {
		err := fmt.Errorf("allocation still points to RP-A %s", rpAUUID)
		log.Error(err, "allocation should not point to RP-A after reshaper")
		return err
	}
	log.Info("Verified allocation points to RP-B after reshaper",
		"consumer", consumerUUID, "rp", rpBUUID)

	// Cleanup: delete allocation, both RPs, and custom resource class.
	log.Info("Cleaning up test resources")
	for _, cleanup := range []struct {
		url  string
		desc string
	}{
		{sc.Endpoint + "/allocations/" + consumerUUID, "allocation"},
		{sc.Endpoint + "/resource_providers/" + rpAUUID, "RP-A"},
		{sc.Endpoint + "/resource_providers/" + rpBUUID, "RP-B"},
		{sc.Endpoint + "/resource_classes/" + testRC, "resource class"},
	} {
		log.Info("Deleting test resource", "desc", cleanup.desc)
		req, err = http.NewRequestWithContext(ctx,
			http.MethodDelete, cleanup.url, http.NoBody)
		if err != nil {
			log.Error(err, "failed to create DELETE request", "desc", cleanup.desc)
			return err
		}
		req.Header.Set("X-Auth-Token", sc.TokenID)
		req.Header.Set("OpenStack-API-Version", apiVersion)
		resp, err = sc.HTTPClient.Do(req)
		if err != nil {
			log.Error(err, "failed to send DELETE request", "desc", cleanup.desc)
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
			log.Error(err, "DELETE returned an error", "desc", cleanup.desc)
			return err
		}
		log.Info("Successfully deleted test resource", "desc", cleanup.desc)
	}

	return nil
}

func init() {
	e2eTests = append(e2eTests, e2eTest{name: "reshaper", run: e2eTestReshaper})
}
