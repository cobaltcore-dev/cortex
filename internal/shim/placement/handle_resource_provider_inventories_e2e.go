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

// e2eTestResourceProviderInventories tests the
// /resource_providers/{uuid}/inventories and
// /resource_providers/{uuid}/inventories/{resource_class} endpoints.
//
//  1. Pre-cleanup: DELETE leftover RP and custom resource class (ignore 404).
//  2. Create fixtures: PUT a custom resource class, POST a test RP.
//  3. GET /{uuid}/inventories — verify the inventory list is empty.
//  4. PUT /{uuid}/inventories — set a full inventory (total=100) and store the
//     returned generation for subsequent requests.
//  5. GET /{uuid}/inventories/{rc} — retrieve the single inventory record and
//     verify total equals 100.
//  6. PUT /{uuid}/inventories/{rc} — update the single inventory total to 200.
//  7. DELETE /{uuid}/inventories/{rc} — remove the single inventory record.
//  8. PUT /{uuid}/inventories — re-add inventory (total=50) for the bulk test.
//  9. DELETE /{uuid}/inventories — bulk-delete all inventories at once.
// 10. Cleanup: DELETE the test RP and custom resource class.
func e2eTestResourceProviderInventories(ctx context.Context) error {
	log := logf.FromContext(ctx)
	log.Info("Running resource provider inventories endpoint e2e test")
	config, err := conf.GetConfig[e2eRootConfig]()
	if err != nil {
		log.Error(err, "failed to get e2e config")
		return err
	}
	log.Info("Creating openstack client for resource provider inventories e2e test")
	sc, err := makeE2EServiceClient(ctx, config)
	if err != nil {
		log.Error(err, "failed to create placement service client for e2e test")
		return err
	}
	log.Info("Successfully created openstack client for resource provider inventories e2e test")

	const testRPUUID = "e2e10000-0000-0000-0000-000000000002"
	const testRPName = "cortex-e2e-test-rp-inv"
	const testRC = "CUSTOM_CORTEX_E2E_INV_RC"
	const apiVersion = "placement 1.26"

	// Pre-cleanup: delete the resource provider (cascades inventories), then
	// the custom resource class. Ignore 404/409.
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
		defer resp.Body.Close()
		log.Info("Pre-cleanup request completed", "url", cleanup.url, "status", resp.StatusCode)
	}

	// Create fixtures: custom resource class and resource provider.
	log.Info("Creating custom resource class for inventories test", "class", testRC)
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

	log.Info("Creating test resource provider for inventories test",
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
	log.Info("Successfully created test resource provider for inventories test",
		"uuid", testRPUUID)

	// Test GET /resource_providers/{uuid}/inventories (empty).
	log.Info("Testing GET /resource_providers/{uuid}/inventories (empty)",
		"uuid", testRPUUID)
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
		Inventories                map[string]json.RawMessage `json:"inventories"`
		ResourceProviderGeneration int                        `json:"resource_provider_generation"`
	}
	err = json.NewDecoder(resp.Body).Decode(&invResp)
	if err != nil {
		log.Error(err, "failed to decode RP inventories response", "uuid", testRPUUID)
		return err
	}
	generation := invResp.ResourceProviderGeneration
	log.Info("Successfully retrieved empty inventories for test resource provider",
		"uuid", testRPUUID, "inventories", len(invResp.Inventories),
		"generation", generation)

	// Test PUT /resource_providers/{uuid}/inventories (set full inventory).
	log.Info("Testing PUT /resource_providers/{uuid}/inventories to set inventory",
		"uuid", testRPUUID, "class", testRC)
	putBody, err := json.Marshal(map[string]any{
		"resource_provider_generation": generation,
		"inventories": map[string]any{
			testRC: map[string]any{
				"total":            100,
				"reserved":         0,
				"min_unit":         1,
				"max_unit":         100,
				"step_size":        1,
				"allocation_ratio": 1.0,
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
	err = json.NewDecoder(resp.Body).Decode(&invResp)
	if err != nil {
		log.Error(err, "failed to decode PUT RP inventories response", "uuid", testRPUUID)
		return err
	}
	generation = invResp.ResourceProviderGeneration
	log.Info("Successfully set inventory on test resource provider",
		"uuid", testRPUUID, "inventories", len(invResp.Inventories),
		"generation", generation)

	// Test GET /resource_providers/{uuid}/inventories/{resource_class}.
	log.Info("Testing GET /resource_providers/{uuid}/inventories/{resource_class}",
		"uuid", testRPUUID, "class", testRC)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet,
		sc.Endpoint+"/resource_providers/"+testRPUUID+"/inventories/"+testRC,
		http.NoBody)
	if err != nil {
		log.Error(err, "failed to create GET request for single inventory",
			"uuid", testRPUUID, "class", testRC)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send GET request for single inventory",
			"uuid", testRPUUID, "class", testRC)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "GET single inventory returned an error",
			"uuid", testRPUUID, "class", testRC)
		return err
	}
	var singleInv struct {
		Total                      int `json:"total"`
		ResourceProviderGeneration int `json:"resource_provider_generation"`
	}
	err = json.NewDecoder(resp.Body).Decode(&singleInv)
	if err != nil {
		log.Error(err, "failed to decode single inventory response",
			"uuid", testRPUUID, "class", testRC)
		return err
	}
	if singleInv.Total != 100 {
		err := fmt.Errorf("expected total 100, got %d", singleInv.Total)
		log.Error(err, "inventory total mismatch", "uuid", testRPUUID, "class", testRC)
		return err
	}
	log.Info("Successfully retrieved single inventory",
		"uuid", testRPUUID, "class", testRC, "total", singleInv.Total)

	// Test PUT /resource_providers/{uuid}/inventories/{resource_class} (update single).
	log.Info("Testing PUT single inventory to update total",
		"uuid", testRPUUID, "class", testRC, "newTotal", 200)
	putBody, err = json.Marshal(map[string]any{
		"resource_provider_generation": generation,
		"total":                        200,
	})
	if err != nil {
		log.Error(err, "failed to marshal request body")
		return err
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut,
		sc.Endpoint+"/resource_providers/"+testRPUUID+"/inventories/"+testRC,
		bytes.NewReader(putBody))
	if err != nil {
		log.Error(err, "failed to create PUT request for single inventory",
			"uuid", testRPUUID, "class", testRC)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send PUT request for single inventory",
			"uuid", testRPUUID, "class", testRC)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "PUT single inventory returned an error",
			"uuid", testRPUUID, "class", testRC)
		return err
	}
	err = json.NewDecoder(resp.Body).Decode(&singleInv)
	if err != nil {
		log.Error(err, "failed to decode PUT single inventory response",
			"uuid", testRPUUID, "class", testRC)
		return err
	}
	generation = singleInv.ResourceProviderGeneration
	log.Info("Successfully updated single inventory",
		"uuid", testRPUUID, "class", testRC, "total", singleInv.Total,
		"generation", generation)

	// Test DELETE /resource_providers/{uuid}/inventories/{resource_class}.
	log.Info("Testing DELETE single inventory",
		"uuid", testRPUUID, "class", testRC)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodDelete,
		sc.Endpoint+"/resource_providers/"+testRPUUID+"/inventories/"+testRC,
		http.NoBody)
	if err != nil {
		log.Error(err, "failed to create DELETE request for single inventory",
			"uuid", testRPUUID, "class", testRC)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send DELETE request for single inventory",
			"uuid", testRPUUID, "class", testRC)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "DELETE single inventory returned an error",
			"uuid", testRPUUID, "class", testRC)
		return err
	}
	log.Info("Successfully deleted single inventory",
		"uuid", testRPUUID, "class", testRC)

	// Get the updated generation after single-item delete for the next PUT.
	log.Info("Getting updated generation after delete", "uuid", testRPUUID)
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
	err = json.NewDecoder(resp.Body).Decode(&invResp)
	if err != nil {
		log.Error(err, "failed to decode RP inventories response", "uuid", testRPUUID)
		return err
	}
	generation = invResp.ResourceProviderGeneration
	log.Info("Got updated generation after delete", "uuid", testRPUUID, "generation", generation)

	// Re-add inventory via PUT all, then test bulk DELETE /inventories.
	log.Info("Re-adding inventory for bulk delete test", "uuid", testRPUUID, "class", testRC)
	putBody, err = json.Marshal(map[string]any{
		"resource_provider_generation": generation,
		"inventories": map[string]any{
			testRC: map[string]any{"total": 50},
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
	log.Info("Successfully re-added inventory for bulk delete test", "uuid", testRPUUID)

	// Test DELETE /resource_providers/{uuid}/inventories (bulk delete).
	log.Info("Testing DELETE /resource_providers/{uuid}/inventories (bulk)",
		"uuid", testRPUUID)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/resource_providers/"+testRPUUID+"/inventories", http.NoBody)
	if err != nil {
		log.Error(err, "failed to create DELETE request for all RP inventories", "uuid", testRPUUID)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send DELETE request for all RP inventories", "uuid", testRPUUID)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "DELETE all RP inventories returned an error", "uuid", testRPUUID)
		return err
	}
	log.Info("Successfully deleted all inventories from test resource provider",
		"uuid", testRPUUID)

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
	e2eTests = append(e2eTests, e2eTest{name: "resource_provider_inventories", run: e2eTestResourceProviderInventories})
}
