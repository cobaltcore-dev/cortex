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

// e2eTestResourceProviderTraits tests the
// /resource_providers/{uuid}/traits endpoints.
//
//  1. Pre-cleanup: DELETE leftover RP traits, RP, and custom trait (ignore 404).
//  2. Create fixtures: PUT a custom trait, POST a test RP.
//  3. GET /{uuid}/traits — verify the trait list is empty, store generation.
//  4. PUT /{uuid}/traits — associate the custom trait with the RP.
//  5. GET /{uuid}/traits — verify the custom trait is now present.
//  6. DELETE /{uuid}/traits — disassociate all traits from the RP.
//  7. GET /{uuid}/traits — verify the trait list is empty again.
//  8. Cleanup: DELETE the test RP and custom trait.
func e2eTestResourceProviderTraits(ctx context.Context) error {
	log := logf.FromContext(ctx)
	log.Info("Running resource provider traits endpoint e2e test")
	config, err := conf.GetConfig[e2eRootConfig]()
	if err != nil {
		log.Error(err, "failed to get e2e config")
		return err
	}
	log.Info("Creating openstack client for resource provider traits e2e test")
	sc, err := makeE2EServiceClient(ctx, config)
	if err != nil {
		log.Error(err, "failed to create placement service client for e2e test")
		return err
	}
	log.Info("Successfully created openstack client for resource provider traits e2e test")

	const testRPUUID = "e2e10000-0000-0000-0000-000000000003"
	const testRPName = "cortex-e2e-test-rp-traits"
	const testTrait = "CUSTOM_CORTEX_E2E_RP_TRAIT"

	// Pre-cleanup: delete RP traits, then RP, then the custom trait.
	log.Info("Pre-cleanup: deleting leftover test resources")
	for _, cleanup := range []struct {
		method string
		url    string
	}{
		{http.MethodDelete, sc.Endpoint + "/resource_providers/" + testRPUUID + "/traits"},
		{http.MethodDelete, sc.Endpoint + "/resource_providers/" + testRPUUID},
		{http.MethodDelete, sc.Endpoint + "/traits/" + testTrait},
	} {
		req, err := http.NewRequestWithContext(ctx, cleanup.method, cleanup.url, http.NoBody)
		if err != nil {
			log.Error(err, "failed to create pre-cleanup request", "url", cleanup.url)
			return err
		}
		req.Header.Set("X-Auth-Token", sc.TokenID)
		req.Header.Set("OpenStack-API-Version", "placement 1.6")
		resp, err := sc.HTTPClient.Do(req)
		if err != nil {
			log.Error(err, "failed to send pre-cleanup request", "url", cleanup.url)
			return err
		}
		defer resp.Body.Close()
		log.Info("Pre-cleanup request completed", "url", cleanup.url, "status", resp.StatusCode)
	}

	// Create fixtures: custom trait and resource provider.
	log.Info("Creating custom trait for RP traits test", "trait", testTrait)
	req, err := http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/traits/"+testTrait, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create PUT request for trait", "trait", testTrait)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	req.Header.Set("Accept", "application/json")
	resp, err := sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send PUT request for trait", "trait", testTrait)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "PUT /traits returned an error", "trait", testTrait)
		return err
	}
	log.Info("Successfully created custom trait", "trait", testTrait)

	log.Info("Creating test resource provider for RP traits test",
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
	log.Info("Successfully created test resource provider for RP traits test",
		"uuid", testRPUUID)

	// Test GET /resource_providers/{uuid}/traits (empty).
	log.Info("Testing GET /resource_providers/{uuid}/traits (empty)", "uuid", testRPUUID)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers/"+testRPUUID+"/traits", http.NoBody)
	if err != nil {
		log.Error(err, "failed to create GET request for RP traits", "uuid", testRPUUID)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send GET request for RP traits", "uuid", testRPUUID)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "GET RP traits returned an error", "uuid", testRPUUID)
		return err
	}
	var traitsResp struct {
		Traits                     []string `json:"traits"`
		ResourceProviderGeneration int      `json:"resource_provider_generation"`
	}
	err = json.NewDecoder(resp.Body).Decode(&traitsResp)
	if err != nil {
		log.Error(err, "failed to decode RP traits response", "uuid", testRPUUID)
		return err
	}
	log.Info("Successfully retrieved empty traits for test resource provider",
		"uuid", testRPUUID, "traits", len(traitsResp.Traits),
		"generation", traitsResp.ResourceProviderGeneration)

	// Test PUT /resource_providers/{uuid}/traits (associate trait).
	log.Info("Testing PUT /resource_providers/{uuid}/traits to associate trait",
		"uuid", testRPUUID, "trait", testTrait)
	putBody, err := json.Marshal(map[string]any{
		"resource_provider_generation": traitsResp.ResourceProviderGeneration,
		"traits":                       []string{testTrait},
	})
	if err != nil {
		log.Error(err, "failed to marshal request body")
		return err
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/resource_providers/"+testRPUUID+"/traits",
		bytes.NewReader(putBody))
	if err != nil {
		log.Error(err, "failed to create PUT request for RP traits", "uuid", testRPUUID)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send PUT request for RP traits", "uuid", testRPUUID)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "PUT RP traits returned an error", "uuid", testRPUUID)
		return err
	}
	log.Info("Successfully associated trait with test resource provider",
		"uuid", testRPUUID, "trait", testTrait)

	// Test GET /resource_providers/{uuid}/traits (after PUT).
	log.Info("Testing GET /resource_providers/{uuid}/traits (after PUT)",
		"uuid", testRPUUID)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers/"+testRPUUID+"/traits", http.NoBody)
	if err != nil {
		log.Error(err, "failed to create GET request for RP traits", "uuid", testRPUUID)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send GET request for RP traits", "uuid", testRPUUID)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "GET RP traits returned an error", "uuid", testRPUUID)
		return err
	}
	err = json.NewDecoder(resp.Body).Decode(&traitsResp)
	if err != nil {
		log.Error(err, "failed to decode RP traits response", "uuid", testRPUUID)
		return err
	}
	if len(traitsResp.Traits) != 1 || traitsResp.Traits[0] != testTrait {
		err := fmt.Errorf("expected trait %s, got %v", testTrait, traitsResp.Traits)
		log.Error(err, "trait mismatch", "uuid", testRPUUID)
		return err
	}
	log.Info("Successfully verified trait on test resource provider",
		"uuid", testRPUUID, "traits", traitsResp.Traits)

	// Test DELETE /resource_providers/{uuid}/traits.
	log.Info("Testing DELETE /resource_providers/{uuid}/traits", "uuid", testRPUUID)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/resource_providers/"+testRPUUID+"/traits", http.NoBody)
	if err != nil {
		log.Error(err, "failed to create DELETE request for RP traits", "uuid", testRPUUID)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send DELETE request for RP traits", "uuid", testRPUUID)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "DELETE RP traits returned an error", "uuid", testRPUUID)
		return err
	}
	log.Info("Successfully deleted traits from test resource provider", "uuid", testRPUUID)

	// Verify traits cleared.
	log.Info("Verifying traits cleared on test resource provider", "uuid", testRPUUID)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers/"+testRPUUID+"/traits", http.NoBody)
	if err != nil {
		log.Error(err, "failed to create GET request for RP traits", "uuid", testRPUUID)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send GET request for RP traits", "uuid", testRPUUID)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "GET RP traits returned an error", "uuid", testRPUUID)
		return err
	}
	err = json.NewDecoder(resp.Body).Decode(&traitsResp)
	if err != nil {
		log.Error(err, "failed to decode RP traits response", "uuid", testRPUUID)
		return err
	}
	if len(traitsResp.Traits) != 0 {
		err := fmt.Errorf("expected 0 traits, got %d", len(traitsResp.Traits))
		log.Error(err, "traits not cleared", "uuid", testRPUUID)
		return err
	}
	log.Info("Verified traits cleared on test resource provider", "uuid", testRPUUID)

	// Cleanup: delete the test resource provider and custom trait.
	log.Info("Cleaning up test resources")
	req, err = http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/resource_providers/"+testRPUUID, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create DELETE request for resource provider", "uuid", testRPUUID)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
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
		http.MethodDelete, sc.Endpoint+"/traits/"+testTrait, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create DELETE request for trait", "trait", testTrait)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send DELETE request for trait", "trait", testTrait)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "DELETE trait returned an error", "trait", testTrait)
		return err
	}
	log.Info("Successfully deleted custom trait", "trait", testTrait)

	return nil
}

func init() {
	e2eTests = append(e2eTests, e2eTest{name: "resource_provider_traits", run: e2eTestResourceProviderTraits})
}
