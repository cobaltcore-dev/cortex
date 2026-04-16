// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// e2eTestTraits tests the /traits and /traits/{name} endpoints.
//
//  1. Pre-cleanup: DELETE any leftover custom test trait (ignore 404).
//  2. GET /traits — list all traits and verify a successful response.
//  3. GET /traits/{name} — retrieve 5 individual existing traits by name.
//  4. PUT /traits/{name} — create a custom test trait (CUSTOM_CORTEX_...).
//  5. DELETE /traits/{name} — remove the custom test trait to clean up.
func e2eTestTraits(ctx context.Context) error {
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

	const testTrait = "CUSTOM_CORTEX_PLACEMENT_SHIM_E2E_TEST_TRAIT"
	const apiVersion = "placement 1.6"

	// Pre-cleanup: delete leftover test trait from a prior run.
	log.Info("Pre-cleanup: deleting leftover test trait", "trait", testTrait)
	req, err := http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/traits/"+testTrait, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create pre-cleanup request")
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	resp, err := sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send pre-cleanup request")
		return err
	}
	defer resp.Body.Close()
	log.Info("Pre-cleanup completed", "status", resp.StatusCode)

	// Test GET /traits
	log.Info("Testing GET /traits endpoint of placement shim")
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/traits", http.NoBody)
	if err != nil {
		log.Error(err, "failed to create request for traits endpoint")
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send request to placement shim /traits endpoint")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "placement shim /traits endpoint returned an error")
		return err
	}
	var list struct {
		Traits []string `json:"traits"`
	}
	err = json.NewDecoder(resp.Body).Decode(&list)
	if err != nil {
		log.Error(err, "failed to decode response from placement shim /traits endpoint")
		return err
	}
	log.Info("Successfully retrieved traits from placement shim",
		"traits", len(list.Traits))

	// Test GET /traits/{name}
	log.Info("Testing GET /traits/{name} endpoint of placement shim")
	traitsToTest := list.Traits
	if len(traitsToTest) > 5 {
		traitsToTest = traitsToTest[:5]
	}
	for _, trait := range traitsToTest { // Test only the first 5 traits to save time.
		log.Info("Testing trait", "trait", trait)
		traitReq, err := http.NewRequestWithContext(ctx,
			http.MethodGet, sc.Endpoint+"/traits/"+trait, http.NoBody)
		if err != nil {
			log.Error(err, "failed to create request for traits/{name} endpoint",
				"trait", trait)
			return err
		}
		traitReq.Header.Set("X-Auth-Token", sc.TokenID)
		traitReq.Header.Set("OpenStack-API-Version", apiVersion)
		traitReq.Header.Set("Accept", "application/json")
		traitResp, err := sc.HTTPClient.Do(traitReq)
		if err != nil {
			log.Error(err, "failed to send request to placement shim /traits/{name} endpoint",
				"trait", trait)
			return err
		}
		if traitResp.StatusCode < 200 || traitResp.StatusCode >= 300 {
			traitResp.Body.Close()
			err := fmt.Errorf("unexpected status code: %d", traitResp.StatusCode)
			log.Error(err, "placement shim /traits/{name} endpoint returned an error",
				"trait", trait)
			return err
		}
		traitResp.Body.Close()
		log.Info("Successfully retrieved trait from placement shim /traits/{name} endpoint",
			"trait", trait)
	}

	// Test PUT /traits/{name}
	log.Info("Testing PUT /traits/{name} endpoint of placement shim", "testTrait", testTrait)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/traits/"+testTrait, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create request for traits/{name} endpoint",
			"trait", testTrait)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send request to placement shim /traits/{name} endpoint",
			"trait", testTrait)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "placement shim /traits/{name} endpoint returned an error",
			"trait", testTrait)
		return err
	}
	log.Info("Successfully created trait with placement shim /traits/{name} endpoint",
		"trait", testTrait)

	// Test DELETE /traits/{name}
	log.Info("Cleaning up test trait from placement shim", "testTrait", testTrait)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/traits/"+testTrait, http.NoBody)
	if err != nil {
		log.Error(err, "failed to create request for traits/{name} endpoint",
			"trait", testTrait)
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", apiVersion)
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send request to placement shim /traits/{name} endpoint",
			"trait", testTrait)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "placement shim /traits/{name} endpoint returned an error",
			"trait", testTrait)
		return err
	}
	log.Info("Successfully deleted test trait with placement shim /traits/{name} endpoint",
		"trait", testTrait)

	return nil
}

func init() {
	e2eTests = append(e2eTests, e2eTest{name: "traits", run: e2eTestTraits})
}
