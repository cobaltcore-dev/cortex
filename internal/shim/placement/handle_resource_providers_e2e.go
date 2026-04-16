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

// e2eTestResourceProviders tests the /resource_providers and
// /resource_providers/{uuid} endpoints.
//
//  1. Pre-cleanup: DELETE any leftover test resource provider (ignore 404).
//  2. GET /resource_providers — list all providers and verify the response.
//  3. POST /resource_providers — create a test provider with a fixed UUID.
//  4. GET /resource_providers/{uuid} — retrieve and verify the created provider.
//  5. PUT /resource_providers/{uuid} — update the provider's name.
//  6. DELETE /resource_providers/{uuid} — remove the test provider.
//  7. GET /resource_providers/{uuid} — confirm deletion returns 404.
func e2eTestResourceProviders(ctx context.Context) error {
	log := logf.FromContext(ctx)
	log.Info("Running resource providers endpoint e2e test")
	config, err := conf.GetConfig[e2eRootConfig]()
	if err != nil {
		log.Error(err, "failed to get e2e config")
		return err
	}
	log.Info("Creating openstack client for resource providers e2e test")
	sc, err := makeE2EServiceClient(ctx, config)
	if err != nil {
		log.Error(err, "failed to create placement service client for e2e test")
		return err
	}
	log.Info("Successfully created openstack client for resource providers e2e test")

	const testRPUUID = "e2e10000-0000-0000-0000-000000000001"
	const testRPName = "cortex-e2e-test-rp"
	const testRPNameUpdated = "cortex-e2e-test-rp-updated"

	// Pre-cleanup: delete any leftover test resource provider from a prior run.
	log.Info("Pre-cleanup: deleting leftover test resource provider", "uuid", testRPUUID)
	req, err := http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/resource_providers/"+testRPUUID, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create pre-cleanup request")
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.20")
	resp, err := sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send pre-cleanup request")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusConflict &&
		(resp.StatusCode < 200 || resp.StatusCode >= 300) {
		err := fmt.Errorf("unexpected status code during pre-cleanup: %d", resp.StatusCode)
		log.Error(err, "pre-cleanup failed")
		return err
	}
	log.Info("Pre-cleanup completed", "status", resp.StatusCode)

	// Test GET /resource_providers
	log.Info("Testing GET /resource_providers endpoint of placement shim")
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers", http.NoBody)
	if err != nil {
		log.Error(err, "failed to create request for resource_providers endpoint")
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.20")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send request to /resource_providers endpoint")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "/resource_providers endpoint returned an error")
		return err
	}
	var listResp struct {
		ResourceProviders []json.RawMessage `json:"resource_providers"`
	}
	err = json.NewDecoder(resp.Body).Decode(&listResp)
	if err != nil {
		log.Error(err, "failed to decode response from /resource_providers endpoint")
		return err
	}
	log.Info("Successfully retrieved resource providers from placement shim",
		"count", len(listResp.ResourceProviders))

	// Test POST /resource_providers (create)
	log.Info("Testing POST /resource_providers to create test provider",
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
	var createdRP struct {
		UUID       string `json:"uuid"`
		Name       string `json:"name"`
		Generation int    `json:"generation"`
	}
	err = json.NewDecoder(resp.Body).Decode(&createdRP)
	if err != nil {
		log.Error(err, "failed to decode POST response from /resource_providers")
		return err
	}
	log.Info("Successfully created test resource provider",
		"uuid", createdRP.UUID, "name", createdRP.Name, "generation", createdRP.Generation)

	// Test GET /resource_providers/{uuid} (show)
	log.Info("Testing GET /resource_providers/{uuid} endpoint", "uuid", testRPUUID)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers/"+testRPUUID, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create GET request for resource provider", "uuid", testRPUUID)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.20")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send GET request for resource provider", "uuid", testRPUUID)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "GET /resource_providers/{uuid} returned an error", "uuid", testRPUUID)
		return err
	}
	log.Info("Successfully retrieved test resource provider", "uuid", testRPUUID)

	// Test PUT /resource_providers/{uuid} (update)
	log.Info("Testing PUT /resource_providers/{uuid} to update provider name",
		"uuid", testRPUUID, "newName", testRPNameUpdated)
	body, err = json.Marshal(map[string]string{
		"name": testRPNameUpdated,
	})
	if err != nil {
		log.Error(err, "failed to marshal request body")
		return err
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/resource_providers/"+testRPUUID, bytes.NewReader(body))
	if err != nil {
		log.Error(err, "failed to create PUT request for resource provider", "uuid", testRPUUID)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.20")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send PUT request for resource provider", "uuid", testRPUUID)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "PUT /resource_providers/{uuid} returned an error", "uuid", testRPUUID)
		return err
	}
	log.Info("Successfully updated test resource provider name",
		"uuid", testRPUUID, "newName", testRPNameUpdated)

	// Cleanup: Test DELETE /resource_providers/{uuid}
	log.Info("Cleaning up test resource provider", "uuid", testRPUUID)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/resource_providers/"+testRPUUID, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create DELETE request for resource provider", "uuid", testRPUUID)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.20")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send DELETE request for resource provider", "uuid", testRPUUID)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "DELETE /resource_providers/{uuid} returned an error", "uuid", testRPUUID)
		return err
	}
	log.Info("Successfully deleted test resource provider", "uuid", testRPUUID)

	// Verify deletion: GET should return 404
	log.Info("Verifying test resource provider was deleted", "uuid", testRPUUID)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers/"+testRPUUID, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create verification GET request", "uuid", testRPUUID)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.20")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send verification GET request", "uuid", testRPUUID)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		err := fmt.Errorf("expected 404 after deletion, got: %d", resp.StatusCode)
		log.Error(err, "resource provider still exists after deletion", "uuid", testRPUUID)
		return err
	}
	log.Info("Verified test resource provider was deleted", "uuid", testRPUUID)

	return nil
}

func init() {
	e2eTests = append(e2eTests, e2eTest{name: "resource_providers", run: e2eTestResourceProviders})
}
