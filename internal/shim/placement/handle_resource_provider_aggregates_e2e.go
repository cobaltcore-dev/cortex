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

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// e2eTestResourceProviderAggregates tests the
// /resource_providers/{uuid}/aggregates endpoints.
//
//  1. Pre-cleanup: DELETE any leftover test RP (ignore 404).
//  2. POST /resource_providers — create a test RP.
//  3. GET /{uuid}/aggregates — verify aggregates are empty, store generation.
//  4. PUT /{uuid}/aggregates — associate two aggregate UUIDs with the RP.
//  5. GET /{uuid}/aggregates — verify both aggregate UUIDs are present.
//  6. PUT /{uuid}/aggregates — clear aggregates by sending an empty list.
//  7. GET /{uuid}/aggregates — verify aggregates are empty after clear.
//  8. Cleanup: DELETE the test RP (also runs via deferred cleanup on failure).
func e2eTestResourceProviderAggregates(ctx context.Context, _ client.Client) error {
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

	const testRPUUID = "e2e10000-0000-0000-0000-000000000004"
	const testRPName = "cortex-e2e-test-rp-agg"
	const testAggUUID1 = "e2e30000-0000-0000-0000-000000000001"
	const testAggUUID2 = "e2e30000-0000-0000-0000-000000000002"

	// Probe: for non-passthrough modes, verify endpoint returns 501.
	unimplemented, err := e2eProbeUnimplemented(ctx, sc, sc.Endpoint+"/resource_providers/"+testRPUUID+"/aggregates")
	if err != nil {
		return fmt.Errorf("probe: %w", err)
	}
	if unimplemented {
		return nil
	}

	// Pre-cleanup: delete any leftover test resource provider from a prior run.
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

	// Deferred cleanup: always delete the test RP on exit so a failed
	// assertion doesn't leave the fixed UUID behind.
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

	// Test GET /resource_providers/{uuid}/aggregates (empty).
	log.Info("Testing GET /resource_providers/{uuid}/aggregates (empty)",
		"uuid", testRPUUID)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers/"+testRPUUID+"/aggregates", http.NoBody)
	if err != nil {
		log.Error(err, "failed to create GET request for RP aggregates", "uuid", testRPUUID)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.19")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send GET request for RP aggregates", "uuid", testRPUUID)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "GET RP aggregates returned an error", "uuid", testRPUUID)
		return err
	}
	var aggResp struct {
		Aggregates                 []string `json:"aggregates"`
		ResourceProviderGeneration int      `json:"resource_provider_generation"`
	}
	err = json.NewDecoder(resp.Body).Decode(&aggResp)
	if err != nil {
		log.Error(err, "failed to decode RP aggregates response", "uuid", testRPUUID)
		return err
	}
	log.Info("Successfully retrieved empty aggregates for test resource provider",
		"uuid", testRPUUID, "aggregates", len(aggResp.Aggregates),
		"generation", aggResp.ResourceProviderGeneration)

	// Test PUT /resource_providers/{uuid}/aggregates (set two aggregates).
	log.Info("Testing PUT /resource_providers/{uuid}/aggregates to set aggregates",
		"uuid", testRPUUID, "agg1", testAggUUID1, "agg2", testAggUUID2)
	putBody, err := json.Marshal(map[string]any{
		"resource_provider_generation": aggResp.ResourceProviderGeneration,
		"aggregates":                   []string{testAggUUID1, testAggUUID2},
	})
	if err != nil {
		log.Error(err, "failed to marshal request body")
		return err
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/resource_providers/"+testRPUUID+"/aggregates",
		bytes.NewReader(putBody))
	if err != nil {
		log.Error(err, "failed to create PUT request for RP aggregates", "uuid", testRPUUID)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.19")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send PUT request for RP aggregates", "uuid", testRPUUID)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "PUT RP aggregates returned an error", "uuid", testRPUUID)
		return err
	}
	var putAggResp struct {
		Aggregates                 []string `json:"aggregates"`
		ResourceProviderGeneration int      `json:"resource_provider_generation"`
	}
	err = json.NewDecoder(resp.Body).Decode(&putAggResp)
	if err != nil {
		log.Error(err, "failed to decode PUT RP aggregates response", "uuid", testRPUUID)
		return err
	}
	log.Info("Successfully set aggregates on test resource provider",
		"uuid", testRPUUID, "aggregates", len(putAggResp.Aggregates),
		"generation", putAggResp.ResourceProviderGeneration)

	// Test GET /resource_providers/{uuid}/aggregates (after PUT).
	log.Info("Testing GET /resource_providers/{uuid}/aggregates (after PUT)",
		"uuid", testRPUUID)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers/"+testRPUUID+"/aggregates", http.NoBody)
	if err != nil {
		log.Error(err, "failed to create GET request for RP aggregates", "uuid", testRPUUID)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.19")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send GET request for RP aggregates", "uuid", testRPUUID)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "GET RP aggregates returned an error", "uuid", testRPUUID)
		return err
	}
	err = json.NewDecoder(resp.Body).Decode(&aggResp)
	if err != nil {
		log.Error(err, "failed to decode RP aggregates response", "uuid", testRPUUID)
		return err
	}
	if len(aggResp.Aggregates) != 2 ||
		!slices.Contains(aggResp.Aggregates, testAggUUID1) ||
		!slices.Contains(aggResp.Aggregates, testAggUUID2) {
		err := fmt.Errorf("expected aggregates %v, got %v",
			[]string{testAggUUID1, testAggUUID2}, aggResp.Aggregates)
		log.Error(err, "aggregate mismatch", "uuid", testRPUUID)
		return err
	}
	log.Info("Successfully verified aggregates on test resource provider",
		"uuid", testRPUUID, "aggregates", aggResp.Aggregates)

	// Clear aggregates by PUT with empty list.
	log.Info("Testing PUT /resource_providers/{uuid}/aggregates to clear aggregates",
		"uuid", testRPUUID)
	putBody, err = json.Marshal(map[string]any{
		"resource_provider_generation": aggResp.ResourceProviderGeneration,
		"aggregates":                   []string{},
	})
	if err != nil {
		log.Error(err, "failed to marshal request body")
		return err
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/resource_providers/"+testRPUUID+"/aggregates",
		bytes.NewReader(putBody))
	if err != nil {
		log.Error(err, "failed to create PUT request to clear RP aggregates", "uuid", testRPUUID)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.19")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send PUT request to clear RP aggregates", "uuid", testRPUUID)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "PUT to clear RP aggregates returned an error", "uuid", testRPUUID)
		return err
	}
	log.Info("Successfully cleared aggregates on test resource provider",
		"uuid", testRPUUID)

	// Verify aggregates are empty after clear.
	log.Info("Verifying aggregates are empty after clear", "uuid", testRPUUID)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers/"+testRPUUID+"/aggregates", http.NoBody)
	if err != nil {
		log.Error(err, "failed to create GET request for RP aggregates", "uuid", testRPUUID)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.19")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send GET request for RP aggregates", "uuid", testRPUUID)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "GET RP aggregates returned an error", "uuid", testRPUUID)
		return err
	}
	err = json.NewDecoder(resp.Body).Decode(&aggResp)
	if err != nil {
		log.Error(err, "failed to decode RP aggregates response", "uuid", testRPUUID)
		return err
	}
	if len(aggResp.Aggregates) != 0 {
		err := fmt.Errorf("expected 0 aggregates after clear, got %d", len(aggResp.Aggregates))
		log.Error(err, "aggregates not empty after clear", "uuid", testRPUUID)
		return err
	}
	log.Info("Verified aggregates are empty after clear", "uuid", testRPUUID)

	// Cleanup: delete the test resource provider.
	log.Info("Cleaning up test resource provider", "uuid", testRPUUID)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/resource_providers/"+testRPUUID, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create DELETE request for resource provider", "uuid", testRPUUID)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.19")
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
	e2eTests = append(e2eTests, e2eTest{name: "resource_provider_aggregates", run: e2eWrapWithModes(e2eTestResourceProviderAggregates)})
}
