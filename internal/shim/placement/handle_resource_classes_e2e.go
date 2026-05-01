// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// e2eTestResourceClasses tests the /resource_classes and
// /resource_classes/{name} endpoints.
//
// Phase 1 — read-only (always runs):
//
//  1. GET /resource_classes — list all resource classes; when mode is
//     passthrough (forwarding to upstream) verify at least one class exists.
//  2. GET /resource_classes/VCPU — verify a standard class is retrievable
//     (skipped when the list is empty).
//  3. GET /resource_classes/{name} — show a nonexistent class and verify 404.
//
// Phase 2 — CRUD (only when mode is non-passthrough):
//
//  1. Pre-cleanup: DELETE any leftover test class (ignore 404).
//  2. PUT /resource_classes/{name} — create a custom test class → 201.
//  3. PUT /resource_classes/{name} — idempotent create → 204.
//  4. GET /resource_classes/{name} — verify the custom class exists → 200.
//  5. DELETE /resource_classes/{name} — remove the custom class → 204.
//  6. GET /resource_classes/{name} — confirm deletion → 404.
//  7. PUT /resource_classes/{name} — bad prefix → 400.
//  8. DELETE /resource_classes/{name} — bad prefix → 400.
func e2eTestResourceClasses(ctx context.Context, _ client.Client) error {
	log := logf.FromContext(ctx)
	log.Info("Running resource classes endpoint e2e test")
	config, err := conf.GetConfig[e2eRootConfig]()
	if err != nil {
		log.Error(err, "failed to get e2e config")
		return err
	}
	log.Info("Creating openstack client for resource classes e2e test")
	sc, err := makeE2EServiceClient(ctx, config)
	if err != nil {
		log.Error(err, "failed to create placement service client for e2e test")
		return err
	}
	log.Info("Successfully created openstack client for resource classes e2e test")

	// ==================== Phase 1: read-only tests ====================

	log.Info("=== Phase 1: read-only resource class tests ===")

	rcMode := e2eCurrentMode(ctx)

	// Test GET /resource_classes
	log.Info("Testing GET /resource_classes endpoint")
	req, err := http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_classes", http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create GET /resource_classes request: %w", err)
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.7")
	req.Header.Set("Accept", "application/json")
	resp, err := sc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send GET /resource_classes request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET /resource_classes: expected 200, got %d", resp.StatusCode)
	}
	var listResp resourceClassesListResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return fmt.Errorf("failed to decode GET /resource_classes response: %w", err)
	}
	if rcMode == FeatureModePassthrough && len(listResp.ResourceClasses) == 0 {
		return errors.New("GET /resource_classes: expected at least one class when forwarding to upstream, got 0")
	}
	log.Info("Successfully retrieved resource classes", "count", len(listResp.ResourceClasses))

	// Test GET /resource_classes/{name} for a known class (skip when list is empty).
	if len(listResp.ResourceClasses) > 0 {
		knownClass := listResp.ResourceClasses[0].Name
		log.Info("Testing GET /resource_classes/{name} for known class", "class", knownClass)
		req, err = http.NewRequestWithContext(ctx,
			http.MethodGet, sc.Endpoint+"/resource_classes/"+knownClass, http.NoBody)
		if err != nil {
			return fmt.Errorf("failed to create GET request for class %s: %w", knownClass, err)
		}
		req.Header.Set("X-Auth-Token", sc.TokenID)
		req.Header.Set("OpenStack-API-Version", "placement 1.7")
		req.Header.Set("Accept", "application/json")
		resp, err = sc.HTTPClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to send GET request for class %s: %w", knownClass, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("GET /resource_classes/%s: expected 200, got %d", knownClass, resp.StatusCode)
		}
		log.Info("Successfully verified known class exists", "class", knownClass)
	} else {
		log.Info("Skipping GET /resource_classes/{name} for known class, list is empty")
	}

	// Test GET /resource_classes/{name} for a nonexistent class.
	log.Info("Testing GET /resource_classes/{name} for nonexistent class")
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_classes/CUSTOM_CORTEX_E2E_NONEXISTENT", http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create GET request for nonexistent class: %w", err)
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.7")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send GET request for nonexistent class: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("GET /resource_classes/CUSTOM_CORTEX_E2E_NONEXISTENT: expected 404, got %d", resp.StatusCode)
	}
	log.Info("Correctly received 404 for nonexistent resource class")

	// ==================== Phase 2: CRUD tests ====================

	log.Info("=== Phase 2: CRUD resource class tests ===")

	const testRC = "CUSTOM_CORTEX_E2E_RC"

	// Pre-cleanup: delete any leftover test class from a prior run.
	log.Info("Pre-cleanup: deleting leftover test resource class", "class", testRC)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/resource_classes/"+testRC, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create pre-cleanup request: %w", err)
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.7")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send pre-cleanup request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("pre-cleanup DELETE /resource_classes/%s: unexpected status %d", testRC, resp.StatusCode)
	}
	log.Info("Pre-cleanup completed", "status", resp.StatusCode)

	// Test PUT /resource_classes/{name} — create → 201.
	log.Info("Testing PUT /resource_classes/{name} to create custom class", "class", testRC)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/resource_classes/"+testRC, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create PUT request for class %s: %w", testRC, err)
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.7")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send PUT request for class %s: %w", testRC, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("PUT /resource_classes/%s (create): expected 201, got %d", testRC, resp.StatusCode)
	}
	log.Info("Successfully created custom resource class", "class", testRC)

	// Test PUT /resource_classes/{name} — idempotent → 204.
	log.Info("Testing PUT /resource_classes/{name} idempotent create", "class", testRC)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/resource_classes/"+testRC, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create idempotent PUT request: %w", err)
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.7")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send idempotent PUT request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("PUT /resource_classes/%s (idempotent): expected 204, got %d", testRC, resp.StatusCode)
	}
	log.Info("Successfully verified idempotent PUT", "class", testRC)

	// Test GET /resource_classes/{name} — verify exists → 200.
	log.Info("Testing GET /resource_classes/{name} for custom class", "class", testRC)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_classes/"+testRC, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create GET request for class %s: %w", testRC, err)
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.7")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send GET request for class %s: %w", testRC, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET /resource_classes/%s: expected 200, got %d", testRC, resp.StatusCode)
	}
	log.Info("Successfully verified custom resource class exists", "class", testRC)

	// Cleanup: DELETE /resource_classes/{name} → 204.
	log.Info("Cleaning up test resource class", "class", testRC)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/resource_classes/"+testRC, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create DELETE request for class %s: %w", testRC, err)
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.7")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send DELETE request for class %s: %w", testRC, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("DELETE /resource_classes/%s: expected 204, got %d", testRC, resp.StatusCode)
	}
	log.Info("Successfully deleted test resource class", "class", testRC)

	// Verify deletion: GET → 404.
	log.Info("Verifying test resource class was deleted", "class", testRC)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_classes/"+testRC, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create verification GET request: %w", err)
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.7")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send verification GET request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("GET /resource_classes/%s after deletion: expected 404, got %d",
			testRC, resp.StatusCode)
	}
	log.Info("Verified test resource class was deleted", "class", testRC)

	// Bad-prefix validation is only enforced by the shim in crd mode.
	if rcMode == FeatureModeCRD {
		// Test PUT /resource_classes/{name} with bad prefix → 400.
		log.Info("Testing PUT /resource_classes/{name} with non-CUSTOM_ prefix")
		req, err = http.NewRequestWithContext(ctx,
			http.MethodPut, sc.Endpoint+"/resource_classes/VCPU_CORTEX_E2E_BAD", http.NoBody)
		if err != nil {
			return fmt.Errorf("failed to create bad-prefix PUT request: %w", err)
		}
		req.Header.Set("X-Auth-Token", sc.TokenID)
		req.Header.Set("OpenStack-API-Version", "placement 1.7")
		resp, err = sc.HTTPClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to send bad-prefix PUT request: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			return fmt.Errorf("PUT /resource_classes/VCPU_CORTEX_E2E_BAD: expected 400, got %d", resp.StatusCode)
		}
		log.Info("Correctly received 400 for PUT with non-CUSTOM_ prefix")

		// Test DELETE /resource_classes/{name} with bad prefix → 400.
		log.Info("Testing DELETE /resource_classes/{name} with non-CUSTOM_ prefix")
		req, err = http.NewRequestWithContext(ctx,
			http.MethodDelete, sc.Endpoint+"/resource_classes/VCPU_CORTEX_E2E_BAD", http.NoBody)
		if err != nil {
			return fmt.Errorf("failed to create bad-prefix DELETE request: %w", err)
		}
		req.Header.Set("X-Auth-Token", sc.TokenID)
		req.Header.Set("OpenStack-API-Version", "placement 1.7")
		resp, err = sc.HTTPClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to send bad-prefix DELETE request: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			return fmt.Errorf("DELETE /resource_classes/VCPU_CORTEX_E2E_BAD: expected 400, got %d", resp.StatusCode)
		}
		log.Info("Correctly received 400 for DELETE with non-CUSTOM_ prefix")
	} else {
		log.Info("Skipping bad-prefix validation tests (only enforced in crd mode)")
	}

	return nil
}

func init() {
	e2eTests = append(e2eTests, e2eTest{name: "resource_classes", run: e2eWrapWithModes(e2eTestResourceClasses)})
}
