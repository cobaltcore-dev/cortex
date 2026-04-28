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
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/gophercloud/gophercloud/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// e2eTestResourceProviders tests the /resource_providers and
// /resource_providers/{uuid} endpoints.
//
// Phase 1 — VMware path (passthrough to upstream placement):
//
//  1. Pre-cleanup: DELETE any leftover test resource provider (ignore 404).
//  2. GET /resource_providers — list all providers and verify the response.
//  3. POST /resource_providers — create a test provider with a fixed UUID.
//  4. GET /resource_providers/{uuid} — retrieve and verify the created provider.
//  5. PUT /resource_providers/{uuid} — update the provider's name.
//  6. DELETE /resource_providers/{uuid} — remove the test provider.
//  7. GET /resource_providers/{uuid} — confirm deletion returns 404.
//
// Phase 2 — KVM path (backed by Hypervisor CRD):
//
//  1. Discover a KVM hypervisor with a non-empty OpenStack ID.
//  2. GET /resource_providers/{kvmUUID} — show translated resource provider.
//  3. PUT /resource_providers/{kvmUUID} — idempotent update with same name → 200.
//  4. PUT /resource_providers/{kvmUUID} — name change → 409.
//  5. DELETE /resource_providers/{kvmUUID} — protected → 409.
//  6. POST /resource_providers — name collision with KVM hypervisor → 409.
//  7. GET /resource_providers — list includes KVM provider (merge test).
func e2eTestResourceProviders(ctx context.Context, cl client.Client) error {
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

	// ==================== Phase 1: VMware path ====================

	// The VMware path creates synthetic test RPs against upstream placement.
	// In crd mode there is no upstream, so skip it.
	mode := e2eCurrentMode(ctx)
	if mode == "" {
		mode = config.Features.ResourceProviders.orDefault()
	}
	if mode != FeatureModeCRD {
		log.Info("=== VMware path: passthrough resource provider tests ===")
		if err := e2eVMwareResourceProviders(ctx, sc); err != nil {
			return fmt.Errorf("VMware path: %w", err)
		}
	} else {
		log.Info("Skipping VMware path because mode is crd (no upstream placement)")
	}

	// ==================== Phase 2: KVM path ====================

	if mode == FeatureModePassthrough {
		log.Info("Skipping KVM resource provider e2e tests because resourceProviders mode is passthrough")
	} else {
		log.Info("=== KVM path: hypervisor-backed resource provider tests ===")
		if err := e2eKVMResourceProviders(ctx, sc, cl); err != nil {
			return fmt.Errorf("KVM path: %w", err)
		}
	}

	return nil
}

func e2eVMwareResourceProviders(ctx context.Context, sc *gophercloud.ServiceClient) error {
	log := logf.FromContext(ctx)

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

func e2eKVMResourceProviders(ctx context.Context, sc *gophercloud.ServiceClient, cl client.Client) error {
	log := logf.FromContext(ctx)

	// Discover a KVM hypervisor with a non-empty OpenStack ID.
	var hvs hv1.HypervisorList
	if err := cl.List(ctx, &hvs); err != nil {
		log.Error(err, "failed to list hypervisors for KVM path")
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
		log.Info("No KVM hypervisors with OpenStack ID found, skipping KVM path tests")
		return nil
	}
	kvmUUID := kvmHV.Status.HypervisorID
	kvmName := kvmHV.Name
	log.Info("Using KVM hypervisor for e2e tests", "uuid", kvmUUID, "name", kvmName)

	// Test GET /resource_providers/{kvmUUID} → 200 with translated RP.
	log.Info("Testing GET /resource_providers/{uuid} for KVM hypervisor", "uuid", kvmUUID)
	req, err := http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers/"+kvmUUID, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create GET request for KVM RP: %w", err)
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.20")
	req.Header.Set("Accept", "application/json")
	resp, err := sc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send GET request for KVM RP: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET /resource_providers/%s: expected 200, got %d", kvmUUID, resp.StatusCode)
	}
	var showRP struct {
		UUID  string `json:"uuid"`
		Name  string `json:"name"`
		Links []struct {
			Href string `json:"href"`
			Rel  string `json:"rel"`
		} `json:"links"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&showRP); err != nil {
		return fmt.Errorf("failed to decode GET KVM RP response: %w", err)
	}
	if showRP.UUID != kvmUUID {
		return fmt.Errorf("GET KVM RP: uuid = %q, want %q", showRP.UUID, kvmUUID)
	}
	if showRP.Name != kvmName {
		return fmt.Errorf("GET KVM RP: name = %q, want %q", showRP.Name, kvmName)
	}
	if len(showRP.Links) == 0 {
		return errors.New("GET KVM RP: expected non-empty links array")
	}
	log.Info("Successfully retrieved KVM resource provider",
		"uuid", showRP.UUID, "name", showRP.Name, "links", len(showRP.Links))

	// Test PUT /resource_providers/{kvmUUID} with same name → 200.
	log.Info("Testing PUT /resource_providers/{uuid} with same name for KVM hypervisor",
		"uuid", kvmUUID, "name", kvmName)
	body, err := json.Marshal(map[string]string{"name": kvmName})
	if err != nil {
		return fmt.Errorf("failed to marshal PUT body: %w", err)
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/resource_providers/"+kvmUUID, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create PUT request for KVM RP: %w", err)
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.20")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send PUT request for KVM RP: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("PUT /resource_providers/%s (same name): expected 200, got %d", kvmUUID, resp.StatusCode)
	}
	log.Info("Successfully performed idempotent PUT on KVM resource provider", "uuid", kvmUUID)

	// Test PUT /resource_providers/{kvmUUID} with different name → 409.
	log.Info("Testing PUT /resource_providers/{uuid} with different name for KVM hypervisor",
		"uuid", kvmUUID)
	body, err = json.Marshal(map[string]string{"name": "cortex-e2e-kvm-renamed"})
	if err != nil {
		return fmt.Errorf("failed to marshal PUT body: %w", err)
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/resource_providers/"+kvmUUID, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create PUT request for KVM RP rename: %w", err)
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.20")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send PUT rename request for KVM RP: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		return fmt.Errorf("PUT /resource_providers/%s (rename): expected 409, got %d", kvmUUID, resp.StatusCode)
	}
	log.Info("Correctly received 409 on KVM resource provider rename", "uuid", kvmUUID)

	// Test DELETE /resource_providers/{kvmUUID} → 409.
	log.Info("Testing DELETE /resource_providers/{uuid} for KVM hypervisor", "uuid", kvmUUID)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/resource_providers/"+kvmUUID, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create DELETE request for KVM RP: %w", err)
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.20")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send DELETE request for KVM RP: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		return fmt.Errorf("DELETE /resource_providers/%s: expected 409, got %d", kvmUUID, resp.StatusCode)
	}
	log.Info("Correctly received 409 on KVM resource provider delete", "uuid", kvmUUID)

	// Test POST /resource_providers with KVM hypervisor name → 409.
	log.Info("Testing POST /resource_providers with KVM hypervisor name", "name", kvmName)
	body, err = json.Marshal(map[string]string{
		"name": kvmName,
		"uuid": "e2e10000-0000-0000-0000-000000000099",
	})
	if err != nil {
		return fmt.Errorf("failed to marshal POST body: %w", err)
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPost, sc.Endpoint+"/resource_providers", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create POST request for KVM name collision: %w", err)
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.20")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send POST request for KVM name collision: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		return fmt.Errorf("POST /resource_providers (KVM name): expected 409, got %d", resp.StatusCode)
	}
	log.Info("Correctly received 409 on POST with KVM hypervisor name", "name", kvmName)

	// Test GET /resource_providers → list includes KVM provider.
	log.Info("Testing GET /resource_providers includes KVM hypervisor in merged list")
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers", http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create GET list request: %w", err)
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.20")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send GET list request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET /resource_providers: expected 200, got %d", resp.StatusCode)
	}
	var listResp struct {
		ResourceProviders []struct {
			UUID string `json:"uuid"`
			Name string `json:"name"`
		} `json:"resource_providers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return fmt.Errorf("failed to decode list response: %w", err)
	}
	found := false
	for _, rp := range listResp.ResourceProviders {
		if rp.UUID == kvmUUID {
			if rp.Name != kvmName {
				return fmt.Errorf("list KVM RP: name = %q, want %q", rp.Name, kvmName)
			}
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("GET /resource_providers: KVM hypervisor %s not found in merged list (%d providers)",
			kvmUUID, len(listResp.ResourceProviders))
	}
	log.Info("Successfully verified KVM hypervisor in merged resource provider list",
		"uuid", kvmUUID, "totalProviders", len(listResp.ResourceProviders))

	return nil
}

func init() {
	e2eTests = append(e2eTests, e2eTest{name: "resource_providers", run: e2eWrapWithModes(e2eTestResourceProviders)})
}
