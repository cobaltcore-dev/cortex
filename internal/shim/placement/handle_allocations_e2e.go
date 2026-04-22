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
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// e2eTestAllocations tests the /allocations/{consumer_uuid} and
// POST /allocations (batch) endpoints.
//
//  1. Pre-cleanup: DELETE leftover consumer allocations, RP, and custom RC.
//  2. Create fixtures: PUT a custom resource class, POST a test RP, PUT
//     inventory on the RP (total=100).
//  3. GET /allocations/{consumer1} — verify allocations are empty.
//  4. PUT /allocations/{consumer1} — create an allocation of 10 units against
//     the test RP using fake project/user IDs.
//  5. GET /allocations/{consumer1} — verify the allocation exists and points
//     to the test RP.
//  6. POST /allocations — batch-create a second consumer's allocation of
//     5 units against the same RP.
//  7. GET /allocations/{consumer2} — verify the second allocation exists.
//  8. DELETE /allocations/{consumer} — remove allocations for both consumers.
//  9. Cleanup: DELETE the test RP and custom resource class.
func e2eTestAllocations(ctx context.Context, _ client.Client) error {
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

	log.Info("Creating test resource provider for allocations test",
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
		log.Error(err, "failed to create GET request for RP inventories")
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send GET request for RP inventories")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "GET RP inventories returned an error")
		return err
	}
	var invResp struct {
		ResourceProviderGeneration int `json:"resource_provider_generation"`
	}
	err = json.NewDecoder(resp.Body).Decode(&invResp)
	if err != nil {
		log.Error(err, "failed to decode RP inventories response")
		return err
	}
	generation := invResp.ResourceProviderGeneration

	// Set inventory on the resource provider.
	log.Info("Setting inventory on test resource provider",
		"uuid", testRPUUID, "class", testRC, "total", 100)
	putBody, err := json.Marshal(map[string]any{
		"resource_provider_generation": generation,
		"inventories": map[string]any{
			testRC: map[string]any{"total": 100},
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
		log.Error(err, "failed to create PUT request for RP inventories")
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send PUT request for RP inventories")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "PUT RP inventories returned an error")
		return err
	}
	log.Info("Successfully set inventory on test resource provider", "uuid", testRPUUID)

	// Test GET /allocations/{consumer_uuid} (empty).
	log.Info("Testing GET /allocations/{consumer_uuid} (empty)", "consumer", consumerUUID1)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/allocations/"+consumerUUID1, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create GET request for allocations", "consumer", consumerUUID1)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send GET request for allocations", "consumer", consumerUUID1)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "GET /allocations returned an error", "consumer", consumerUUID1)
		return err
	}
	var allocResp struct {
		Allocations map[string]json.RawMessage `json:"allocations"`
	}
	err = json.NewDecoder(resp.Body).Decode(&allocResp)
	if err != nil {
		log.Error(err, "failed to decode allocations response", "consumer", consumerUUID1)
		return err
	}
	log.Info("Successfully retrieved empty allocations for consumer",
		"consumer", consumerUUID1, "allocationCount", len(allocResp.Allocations))

	// Test PUT /allocations/{consumer_uuid} (create allocation).
	log.Info("Testing PUT /allocations/{consumer_uuid} to create allocation",
		"consumer", consumerUUID1, "rp", testRPUUID, "amount", 10)
	allocBody, err := json.Marshal(map[string]any{
		"allocations": map[string]any{
			testRPUUID: map[string]any{
				"resources": map[string]int{
					testRC: 10,
				},
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
		http.MethodPut, sc.Endpoint+"/allocations/"+consumerUUID1,
		bytes.NewReader(allocBody))
	if err != nil {
		log.Error(err, "failed to create PUT request for allocations", "consumer", consumerUUID1)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send PUT request for allocations", "consumer", consumerUUID1)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "PUT /allocations returned an error", "consumer", consumerUUID1)
		return err
	}
	log.Info("Successfully created allocation for consumer",
		"consumer", consumerUUID1, "rp", testRPUUID)

	// Test GET /allocations/{consumer_uuid} (after PUT).
	log.Info("Testing GET /allocations/{consumer_uuid} (after PUT)",
		"consumer", consumerUUID1)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/allocations/"+consumerUUID1, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create GET request for allocations", "consumer", consumerUUID1)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send GET request for allocations", "consumer", consumerUUID1)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "GET /allocations returned an error", "consumer", consumerUUID1)
		return err
	}
	err = json.NewDecoder(resp.Body).Decode(&allocResp)
	if err != nil {
		log.Error(err, "failed to decode allocations response", "consumer", consumerUUID1)
		return err
	}
	if _, ok := allocResp.Allocations[testRPUUID]; !ok {
		err := fmt.Errorf("expected allocation against RP %s", testRPUUID)
		log.Error(err, "allocation not found", "consumer", consumerUUID1)
		return err
	}
	log.Info("Successfully verified allocation for consumer",
		"consumer", consumerUUID1, "allocationCount", len(allocResp.Allocations))

	// Test POST /allocations (batch manage) — create a second consumer.
	log.Info("Testing POST /allocations (batch) to create second consumer allocation",
		"consumer", consumerUUID2, "rp", testRPUUID, "amount", 5)
	batchBody, err := json.Marshal(map[string]any{
		consumerUUID2: map[string]any{
			"allocations": map[string]any{
				testRPUUID: map[string]any{
					"resources": map[string]int{
						testRC: 5,
					},
				},
			},
			"project_id":          projectID,
			"user_id":             userID,
			"consumer_generation": nil,
		},
	})
	if err != nil {
		log.Error(err, "failed to marshal request body")
		return err
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPost, sc.Endpoint+"/allocations",
		bytes.NewReader(batchBody))
	if err != nil {
		log.Error(err, "failed to create POST request for batch allocations")
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send POST request for batch allocations")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "POST /allocations returned an error")
		return err
	}
	log.Info("Successfully created batch allocation for second consumer",
		"consumer", consumerUUID2)

	// Verify the second consumer's allocation.
	log.Info("Verifying second consumer's allocation", "consumer", consumerUUID2)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/allocations/"+consumerUUID2, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create GET request for allocations", "consumer", consumerUUID2)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send GET request for allocations", "consumer", consumerUUID2)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "GET /allocations returned an error", "consumer", consumerUUID2)
		return err
	}
	err = json.NewDecoder(resp.Body).Decode(&allocResp)
	if err != nil {
		log.Error(err, "failed to decode allocations response", "consumer", consumerUUID2)
		return err
	}
	if _, ok := allocResp.Allocations[testRPUUID]; !ok {
		err := fmt.Errorf("expected allocation against RP %s", testRPUUID)
		log.Error(err, "allocation not found for second consumer", "consumer", consumerUUID2)
		return err
	}
	log.Info("Successfully verified second consumer's allocation",
		"consumer", consumerUUID2)

	// Test DELETE /allocations/{consumer_uuid} for both consumers.
	for _, consumer := range []string{consumerUUID1, consumerUUID2} {
		log.Info("Testing DELETE /allocations/{consumer_uuid}", "consumer", consumer)
		req, err = http.NewRequestWithContext(ctx,
			http.MethodDelete, sc.Endpoint+"/allocations/"+consumer, http.NoBody)
		if err != nil {
			log.Error(err, "failed to create DELETE request for allocations", "consumer", consumer)
			return err
		}
		req.Header.Set("X-Auth-Token", sc.TokenID)
		req.Header.Set("OpenStack-API-Version", apiVersion)
		resp, err = sc.HTTPClient.Do(req)
		if err != nil {
			log.Error(err, "failed to send DELETE request for allocations", "consumer", consumer)
			return err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
			log.Error(err, "DELETE /allocations returned an error", "consumer", consumer)
			return err
		}
		resp.Body.Close()
		log.Info("Successfully deleted allocation for consumer", "consumer", consumer)
	}

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
	e2eTests = append(e2eTests, e2eTest{name: "allocations", run: e2eTestAllocations})
}
