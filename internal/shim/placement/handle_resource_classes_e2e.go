// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// e2eTestResourceClasses tests the /resource_classes and
// /resource_classes/{name} endpoints.
//
//  1. Pre-cleanup: DELETE any leftover custom resource class (ignore 404).
//  2. GET /resource_classes — list all classes and verify the response.
//  3. GET /resource_classes/VCPU — confirm a standard class is retrievable.
//  4. PUT /resource_classes/{name} — create a custom test class.
//  5. GET /resource_classes/{name} — verify the custom class now exists.
//  6. DELETE /resource_classes/{name} — remove the custom class.
//  7. GET /resource_classes/{name} — confirm deletion returns 404.
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

	const testRC = "CUSTOM_CORTEX_E2E_RC"

	// Probe: for non-passthrough modes, verify endpoint returns 501.
	unimplemented, err := e2eProbeUnimplemented(ctx, sc, sc.Endpoint+"/resource_classes")
	if err != nil {
		return fmt.Errorf("probe: %w", err)
	}
	if unimplemented {
		return nil
	}

	// Pre-cleanup: delete any leftover test resource class from a prior run.
	log.Info("Pre-cleanup: deleting leftover test resource class", "class", testRC)
	req, err := http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/resource_classes/"+testRC, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create pre-cleanup request")
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.7")
	resp, err := sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send pre-cleanup request")
		return err
	}
	defer resp.Body.Close()
	// Ignore 404 (not found) — that's expected if no leftover exists.
	if resp.StatusCode != http.StatusNotFound &&
		(resp.StatusCode < 200 || resp.StatusCode >= 300) {
		err := fmt.Errorf("unexpected status code during pre-cleanup: %d", resp.StatusCode)
		log.Error(err, "pre-cleanup failed")
		return err
	}
	log.Info("Pre-cleanup completed", "status", resp.StatusCode)

	// Test GET /resource_classes
	log.Info("Testing GET /resource_classes endpoint of placement shim")
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_classes", http.NoBody)
	if err != nil {
		log.Error(err, "failed to create request for resource_classes endpoint")
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.7")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send request to /resource_classes endpoint")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "/resource_classes endpoint returned an error")
		return err
	}
	var list struct {
		ResourceClasses []struct {
			Name string `json:"name"`
		} `json:"resource_classes"`
	}
	err = json.NewDecoder(resp.Body).Decode(&list)
	if err != nil {
		log.Error(err, "failed to decode response from /resource_classes endpoint")
		return err
	}
	log.Info("Successfully retrieved resource classes from placement shim",
		"count", len(list.ResourceClasses))

	// Test GET /resource_classes/{name} for a standard class
	log.Info("Testing GET /resource_classes/VCPU endpoint of placement shim")
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_classes/VCPU", http.NoBody)
	if err != nil {
		log.Error(err, "failed to create request for resource_classes/VCPU endpoint")
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.7")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send request to /resource_classes/VCPU endpoint")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "/resource_classes/VCPU endpoint returned an error")
		return err
	}
	log.Info("Successfully retrieved standard resource class VCPU from placement shim")

	// Test PUT /resource_classes/{name} (create custom class)
	log.Info("Testing PUT /resource_classes/{name} to create custom class", "class", testRC)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/resource_classes/"+testRC, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create PUT request for resource_classes", "class", testRC)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.7")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send PUT request to /resource_classes", "class", testRC)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "PUT /resource_classes returned an error", "class", testRC)
		return err
	}
	log.Info("Successfully created custom resource class", "class", testRC,
		"status", resp.StatusCode)

	// Test GET /resource_classes/{name} for the custom class
	log.Info("Testing GET /resource_classes/{name} for custom class", "class", testRC)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_classes/"+testRC, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create GET request for custom resource class", "class", testRC)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.7")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send GET request for custom resource class", "class", testRC)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "GET custom resource class returned an error", "class", testRC)
		return err
	}
	log.Info("Successfully verified custom resource class exists", "class", testRC)

	// Cleanup: Test DELETE /resource_classes/{name}
	log.Info("Cleaning up test resource class from placement shim", "class", testRC)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/resource_classes/"+testRC, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create DELETE request for resource class", "class", testRC)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.7")
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
	log.Info("Successfully deleted test resource class", "class", testRC)

	// Verify deletion: GET should return 404
	log.Info("Verifying test resource class was deleted", "class", testRC)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_classes/"+testRC, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create verification GET request", "class", testRC)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.7")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send verification GET request", "class", testRC)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		err := fmt.Errorf("expected 404 after deletion, got: %d", resp.StatusCode)
		log.Error(err, "resource class still exists after deletion", "class", testRC)
		return err
	}
	log.Info("Verified test resource class was deleted", "class", testRC)

	return nil
}

func init() {
	e2eTests = append(e2eTests, e2eTest{name: "resource_classes", run: e2eWrapWithModes(e2eTestResourceClasses)})
}
