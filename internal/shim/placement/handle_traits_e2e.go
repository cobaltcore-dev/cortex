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

// e2eTestTraits tests the /traits and /traits/{name} endpoints.
//
// Phase 1 — read-only (always runs):
//
//  1. GET /traits — list all traits; when traits mode is passthrough
//     (forwarding to upstream) verify at least one trait exists.
//  2. GET /traits/{name} — show a known trait from the list and verify 200
//     (skipped when the trait list is empty).
//  3. GET /traits/{name} — show a nonexistent trait and verify 404.
//
// Phase 2 — CRUD (only when traits mode is non-passthrough):
//
//  1. Pre-cleanup: DELETE any leftover test trait (ignore 404).
//  2. PUT /traits/{name} — create a custom test trait → 201.
//  3. PUT /traits/{name} — idempotent create → 204.
//  4. GET /traits/{name} — verify the custom trait exists → 200.
//  5. GET /traits?name=in:{name} — verify the trait appears in filtered list.
//  6. DELETE /traits/{name} — remove the custom trait → 204.
//  7. GET /traits/{name} — confirm deletion → 404.
//  8. PUT /traits/{name} — bad prefix → 400.
//  9. DELETE /traits/{name} — bad prefix → 400.
func e2eTestTraits(ctx context.Context, _ client.Client) error {
	log := logf.FromContext(ctx)
	log.Info("Running traits endpoint e2e test")
	config, err := conf.GetConfig[e2eRootConfig]()
	if err != nil {
		log.Error(err, "failed to get e2e config")
		return err
	}
	log.Info("Creating openstack client for traits e2e test")
	sc, err := makeE2EServiceClient(ctx, config)
	if err != nil {
		log.Error(err, "failed to create placement service client for e2e test")
		return err
	}
	log.Info("Successfully created openstack client for traits e2e test")

	// ==================== Phase 1: read-only tests ====================

	log.Info("=== Phase 1: read-only trait tests ===")

	// Test GET /traits
	log.Info("Testing GET /traits endpoint")
	req, err := http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/traits", http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create GET /traits request: %w", err)
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	req.Header.Set("Accept", "application/json")
	resp, err := sc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send GET /traits request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET /traits: expected 200, got %d", resp.StatusCode)
	}
	var listResp struct {
		Traits []string `json:"traits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return fmt.Errorf("failed to decode GET /traits response: %w", err)
	}
	// When traits are served locally (hybrid or crd mode) the list may be
	// empty. Only require at least one trait when forwarding to upstream
	// placement, which always has standard traits.
	traitsMode := e2eCurrentMode(ctx)
	if traitsMode == FeatureModePassthrough && len(listResp.Traits) == 0 {
		return errors.New("GET /traits: expected at least one trait, got 0")
	}
	log.Info("Successfully retrieved traits", "count", len(listResp.Traits))

	// Test GET /traits/{name} for a known trait (skip when the list is empty).
	if len(listResp.Traits) > 0 {
		knownTrait := listResp.Traits[0]
		log.Info("Testing GET /traits/{name} for known trait", "trait", knownTrait)
		req, err = http.NewRequestWithContext(ctx,
			http.MethodGet, sc.Endpoint+"/traits/"+knownTrait, http.NoBody)
		if err != nil {
			return fmt.Errorf("failed to create GET request for trait %s: %w", knownTrait, err)
		}
		req.Header.Set("X-Auth-Token", sc.TokenID)
		req.Header.Set("OpenStack-API-Version", "placement 1.6")
		resp, err = sc.HTTPClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to send GET request for trait %s: %w", knownTrait, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			return fmt.Errorf("GET /traits/%s: expected 204, got %d", knownTrait, resp.StatusCode)
		}
		log.Info("Successfully verified known trait exists", "trait", knownTrait)
	} else {
		log.Info("Skipping GET /traits/{name} for known trait, trait list is empty")
	}

	// Test GET /traits/{name} for a nonexistent trait.
	log.Info("Testing GET /traits/{name} for nonexistent trait")
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/traits/CUSTOM_CORTEX_E2E_NONEXISTENT", http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create GET request for nonexistent trait: %w", err)
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send GET request for nonexistent trait: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("GET /traits/CUSTOM_CORTEX_E2E_NONEXISTENT: expected 404, got %d", resp.StatusCode)
	}
	log.Info("Correctly received 404 for nonexistent trait")

	// ==================== Phase 2: CRUD tests ====================

	log.Info("=== Phase 2: CRUD trait tests ===")

	const testTrait = "CUSTOM_CORTEX_E2E_TRAIT"

	// Pre-cleanup: delete any leftover test trait from a prior run.
	log.Info("Pre-cleanup: deleting leftover test trait", "trait", testTrait)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/traits/"+testTrait, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create pre-cleanup request: %w", err)
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send pre-cleanup request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("pre-cleanup DELETE /traits/%s: unexpected status %d", testTrait, resp.StatusCode)
	}
	log.Info("Pre-cleanup completed", "status", resp.StatusCode)

	// Test PUT /traits/{name} — create → 201.
	log.Info("Testing PUT /traits/{name} to create custom trait", "trait", testTrait)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/traits/"+testTrait, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create PUT request for trait %s: %w", testTrait, err)
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send PUT request for trait %s: %w", testTrait, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("PUT /traits/%s (create): expected 201, got %d", testTrait, resp.StatusCode)
	}
	log.Info("Successfully created custom trait", "trait", testTrait)

	// Test PUT /traits/{name} — idempotent → 204.
	log.Info("Testing PUT /traits/{name} idempotent create", "trait", testTrait)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/traits/"+testTrait, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create idempotent PUT request: %w", err)
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send idempotent PUT request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("PUT /traits/%s (idempotent): expected 204, got %d", testTrait, resp.StatusCode)
	}
	log.Info("Successfully verified idempotent PUT", "trait", testTrait)

	// Test GET /traits/{name} — verify exists → 204.
	log.Info("Testing GET /traits/{name} for custom trait", "trait", testTrait)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/traits/"+testTrait, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create GET request for trait %s: %w", testTrait, err)
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send GET request for trait %s: %w", testTrait, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("GET /traits/%s: expected 204, got %d", testTrait, resp.StatusCode)
	}
	log.Info("Successfully verified custom trait exists", "trait", testTrait)

	// Test GET /traits?name=in:{name} — filtered list.
	log.Info("Testing GET /traits with in: filter", "trait", testTrait)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/traits?name=in:"+testTrait, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create filtered GET request: %w", err)
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send filtered GET request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET /traits?name=in:%s: expected 200, got %d", testTrait, resp.StatusCode)
	}
	var filteredResp struct {
		Traits []string `json:"traits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&filteredResp); err != nil {
		return fmt.Errorf("failed to decode filtered traits response: %w", err)
	}
	if len(filteredResp.Traits) != 1 || filteredResp.Traits[0] != testTrait {
		return fmt.Errorf("GET /traits?name=in:%s: expected [%s], got %v",
			testTrait, testTrait, filteredResp.Traits)
	}
	log.Info("Successfully verified trait in filtered list", "trait", testTrait)

	// Cleanup: DELETE /traits/{name} → 204.
	log.Info("Cleaning up test trait", "trait", testTrait)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/traits/"+testTrait, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create DELETE request for trait %s: %w", testTrait, err)
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send DELETE request for trait %s: %w", testTrait, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("DELETE /traits/%s: expected 204, got %d", testTrait, resp.StatusCode)
	}
	log.Info("Successfully deleted test trait", "trait", testTrait)

	// Verify deletion: GET → 404.
	log.Info("Verifying test trait was deleted", "trait", testTrait)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/traits/"+testTrait, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create verification GET request: %w", err)
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send verification GET request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("GET /traits/%s after deletion: expected 404, got %d",
			testTrait, resp.StatusCode)
	}
	log.Info("Verified test trait was deleted", "trait", testTrait)

	// Bad-prefix validation is only enforced by the shim in crd mode.
	// In hybrid mode, writes forward to upstream which has different behavior.
	if traitsMode == FeatureModeCRD {
		// Test PUT /traits/{name} with bad prefix → 400.
		log.Info("Testing PUT /traits/{name} with non-CUSTOM_ prefix")
		req, err = http.NewRequestWithContext(ctx,
			http.MethodPut, sc.Endpoint+"/traits/HW_CORTEX_E2E_BAD", http.NoBody)
		if err != nil {
			return fmt.Errorf("failed to create bad-prefix PUT request: %w", err)
		}
		req.Header.Set("X-Auth-Token", sc.TokenID)
		req.Header.Set("OpenStack-API-Version", "placement 1.6")
		resp, err = sc.HTTPClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to send bad-prefix PUT request: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			return fmt.Errorf("PUT /traits/HW_CORTEX_E2E_BAD: expected 400, got %d", resp.StatusCode)
		}
		log.Info("Correctly received 400 for PUT with non-CUSTOM_ prefix")

		// Test DELETE /traits/{name} with bad prefix → 400.
		log.Info("Testing DELETE /traits/{name} with non-CUSTOM_ prefix")
		req, err = http.NewRequestWithContext(ctx,
			http.MethodDelete, sc.Endpoint+"/traits/HW_CORTEX_E2E_BAD", http.NoBody)
		if err != nil {
			return fmt.Errorf("failed to create bad-prefix DELETE request: %w", err)
		}
		req.Header.Set("X-Auth-Token", sc.TokenID)
		req.Header.Set("OpenStack-API-Version", "placement 1.6")
		resp, err = sc.HTTPClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to send bad-prefix DELETE request: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			return fmt.Errorf("DELETE /traits/HW_CORTEX_E2E_BAD: expected 400, got %d", resp.StatusCode)
		}
		log.Info("Correctly received 400 for DELETE with non-CUSTOM_ prefix")
	} else {
		log.Info("Skipping bad-prefix validation tests (only enforced in crd mode)")
	}

	return nil
}

func init() {
	e2eTests = append(e2eTests, e2eTest{name: "traits", run: e2eWrapWithModes(e2eTestTraits)})
}
