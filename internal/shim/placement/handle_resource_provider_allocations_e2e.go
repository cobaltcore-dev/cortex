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

// e2eTestResourceProviderAllocations tests the
// /resource_providers/{uuid}/allocations endpoint.
//
//  1. Pre-cleanup: DELETE any leftover test RP (ignore 404).
//  2. POST /resource_providers — create a test RP.
//  3. GET /{uuid}/allocations — retrieve allocations for the test RP (expect
//     an empty allocations map since nothing has been allocated).
//  4. GET /resource_providers — list real providers, then GET /{uuid}/allocations
//     on up to 3 of them to verify the endpoint works with production data.
//  5. Cleanup: DELETE the test RP.
func e2eTestResourceProviderAllocations(ctx context.Context) error {
	log := logf.FromContext(ctx)
	log.Info("Running resource provider allocations endpoint e2e test")
	config, err := conf.GetConfig[e2eRootConfig]()
	if err != nil {
		log.Error(err, "failed to get e2e config")
		return err
	}
	log.Info("Creating openstack client for resource provider allocations e2e test")
	sc, err := makeE2EServiceClient(ctx, config)
	if err != nil {
		log.Error(err, "failed to create placement service client for e2e test")
		return err
	}
	log.Info("Successfully created openstack client for resource provider allocations e2e test")

	const testRPUUID = "e2e10000-0000-0000-0000-000000000006"
	const testRPName = "cortex-e2e-test-rp-alloc-view"

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

	// Create a test resource provider.
	log.Info("Creating test resource provider for allocations view test",
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
	log.Info("Successfully created test resource provider for allocations view test",
		"uuid", testRPUUID)

	// Test GET /resource_providers/{uuid}/allocations on our test provider.
	log.Info("Testing GET /resource_providers/{uuid}/allocations on test provider",
		"uuid", testRPUUID)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers/"+testRPUUID+"/allocations", http.NoBody)
	if err != nil {
		log.Error(err, "failed to create GET request for RP allocations", "uuid", testRPUUID)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.20")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send GET request for RP allocations", "uuid", testRPUUID)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "GET RP allocations returned an error", "uuid", testRPUUID)
		return err
	}
	var allocResp struct {
		Allocations                map[string]json.RawMessage `json:"allocations"`
		ResourceProviderGeneration int                        `json:"resource_provider_generation"`
	}
	err = json.NewDecoder(resp.Body).Decode(&allocResp)
	if err != nil {
		log.Error(err, "failed to decode RP allocations response", "uuid", testRPUUID)
		return err
	}
	log.Info("Successfully retrieved allocations for test resource provider",
		"uuid", testRPUUID, "allocationCount", len(allocResp.Allocations))

	// Also test against existing real resource providers.
	log.Info("Testing GET /resource_providers/{uuid}/allocations on existing providers")
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers", http.NoBody)
	if err != nil {
		log.Error(err, "failed to create request for listing resource providers")
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.20")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send request to list resource providers")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "list resource providers returned an error")
		return err
	}
	var rpList struct {
		ResourceProviders []struct {
			UUID string `json:"uuid"`
		} `json:"resource_providers"`
	}
	err = json.NewDecoder(resp.Body).Decode(&rpList)
	if err != nil {
		log.Error(err, "failed to decode resource providers list")
		return err
	}
	for i, rp := range rpList.ResourceProviders {
		if i >= 3 {
			break
		}
		log.Info("Testing GET allocations on existing resource provider", "uuid", rp.UUID)
		rpReq, err := http.NewRequestWithContext(ctx,
			http.MethodGet, sc.Endpoint+"/resource_providers/"+rp.UUID+"/allocations", http.NoBody)
		if err != nil {
			log.Error(err, "failed to create GET request for RP allocations", "uuid", rp.UUID)
			return err
		}
		rpReq.Header.Set("X-Auth-Token", sc.TokenID)
		rpReq.Header.Set("OpenStack-API-Version", "placement 1.20")
		rpReq.Header.Set("Accept", "application/json")
		rpResp, err := sc.HTTPClient.Do(rpReq)
		if err != nil {
			log.Error(err, "failed to send GET request for RP allocations", "uuid", rp.UUID)
			return err
		}
		if rpResp.StatusCode < 200 || rpResp.StatusCode >= 300 {
			rpResp.Body.Close()
			err := fmt.Errorf("unexpected status code: %d", rpResp.StatusCode)
			log.Error(err, "GET RP allocations returned an error", "uuid", rp.UUID)
			return err
		}
		rpResp.Body.Close()
		log.Info("Successfully retrieved allocations for existing resource provider",
			"uuid", rp.UUID)
	}

	// Cleanup: delete the test resource provider.
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

	return nil
}

func init() {
	e2eTests = append(e2eTests, e2eTest{name: "resource_provider_allocations", run: e2eTestResourceProviderAllocations})
}
