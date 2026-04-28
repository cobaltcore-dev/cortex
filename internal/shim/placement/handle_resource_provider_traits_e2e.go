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
	"time"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/gophercloud/gophercloud/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// e2eTestResourceProviderTraits tests the
// /resource_providers/{uuid}/traits endpoints.
//
// In passthrough mode: exercises the upstream placement path with a
// dynamically created resource provider.
// In hybrid/crd mode: exercises the spec.groups-backed CRD path using a
// real KVM hypervisor discovered from the cluster.
func e2eTestResourceProviderTraits(ctx context.Context, cl client.Client) error {
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

	mode := e2eCurrentMode(ctx)
	switch mode {
	case FeatureModePassthrough:
		return e2ePassthroughResourceProviderTraits(ctx, sc)
	case FeatureModeHybrid, FeatureModeCRD:
		return e2eCRDResourceProviderTraits(ctx, sc, cl)
	default:
		return fmt.Errorf("unexpected mode %q", mode)
	}
}

func e2ePassthroughResourceProviderTraits(ctx context.Context, sc *gophercloud.ServiceClient) error {
	log := logf.FromContext(ctx)

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
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound &&
			(resp.StatusCode < 200 || resp.StatusCode >= 300) {
			err := fmt.Errorf("unexpected status code during pre-cleanup: %d", resp.StatusCode)
			log.Error(err, "pre-cleanup failed", "url", cleanup.url)
			return err
		}
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

	// Deferred cleanup.
	defer func() {
		log.Info("Deferred cleanup: deleting test resources")
		for _, c := range []struct {
			url  string
			desc string
		}{
			{sc.Endpoint + "/resource_providers/" + testRPUUID + "/traits", "RP traits"},
			{sc.Endpoint + "/resource_providers/" + testRPUUID, "RP"},
			{sc.Endpoint + "/traits/" + testTrait, "trait"},
		} {
			dReq, dErr := http.NewRequestWithContext(ctx, http.MethodDelete, c.url, http.NoBody)
			if dErr != nil {
				log.Error(dErr, "deferred cleanup: failed to create request", "desc", c.desc)
				continue
			}
			dReq.Header.Set("X-Auth-Token", sc.TokenID)
			dReq.Header.Set("OpenStack-API-Version", "placement 1.6")
			dResp, dErr := sc.HTTPClient.Do(dReq)
			if dErr != nil {
				log.Error(dErr, "deferred cleanup: failed to send request", "desc", c.desc)
				continue
			}
			dResp.Body.Close()
			log.Info("Deferred cleanup completed", "desc", c.desc, "status", dResp.StatusCode)
		}
	}()

	// Test GET (empty).
	log.Info("Testing GET /resource_providers/{uuid}/traits (empty)", "uuid", testRPUUID)
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers/"+testRPUUID+"/traits", http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET RP traits: unexpected status %d", resp.StatusCode)
	}
	var traitsResp struct {
		Traits                     []string `json:"traits"`
		ResourceProviderGeneration int      `json:"resource_provider_generation"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&traitsResp); err != nil {
		return err
	}
	if len(traitsResp.Traits) != 0 {
		return fmt.Errorf("expected 0 initial traits, got %d", len(traitsResp.Traits))
	}
	log.Info("Verified empty traits", "generation", traitsResp.ResourceProviderGeneration)

	// Test PUT (associate trait).
	putBody, err := json.Marshal(map[string]any{
		"resource_provider_generation": traitsResp.ResourceProviderGeneration,
		"traits":                       []string{testTrait},
	})
	if err != nil {
		return err
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/resource_providers/"+testRPUUID+"/traits",
		bytes.NewReader(putBody))
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("PUT RP traits: unexpected status %d", resp.StatusCode)
	}
	log.Info("Successfully associated trait")

	// Test GET (after PUT).
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers/"+testRPUUID+"/traits", http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&traitsResp); err != nil {
		return err
	}
	if !slices.Contains(traitsResp.Traits, testTrait) {
		return fmt.Errorf("expected trait %s, got %v", testTrait, traitsResp.Traits)
	}
	log.Info("Verified trait present after PUT")

	// Test DELETE.
	req, err = http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/resource_providers/"+testRPUUID+"/traits", http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("DELETE RP traits: unexpected status %d", resp.StatusCode)
	}
	log.Info("Successfully deleted traits")

	// Cleanup.
	req, err = http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/resource_providers/"+testRPUUID, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()

	req, err = http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/traits/"+testTrait, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()

	return nil
}

// e2eCRDResourceProviderTraits tests the CRD/hybrid path by discovering a
// real KVM hypervisor in the cluster, seeding spec.groups, and exercising
// GET/PUT/DELETE through the shim.
func e2eCRDResourceProviderTraits(ctx context.Context, sc *gophercloud.ServiceClient, cl client.Client) error {
	log := logf.FromContext(ctx)

	// Discover a KVM hypervisor with a non-empty OpenStack ID.
	var hvs hv1.HypervisorList
	if err := cl.List(ctx, &hvs); err != nil {
		log.Error(err, "failed to list hypervisors for CRD traits path")
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
		log.Info("No KVM hypervisors with OpenStack ID found, skipping CRD traits tests")
		return nil
	}
	kvmUUID := kvmHV.Status.HypervisorID
	log.Info("Using KVM hypervisor for CRD traits e2e tests", "uuid", kvmUUID, "name", kvmHV.Name)

	// Save original groups for restoration.
	originalGroups := kvmHV.Spec.Groups

	// Seed spec.groups with test traits (preserve non-trait groups).
	const testTrait1 = "CUSTOM_E2E_CRD_TRAIT_1"
	const testTrait2 = "CUSTOM_E2E_CRD_TRAIT_2"
	var nonTraitGroups []hv1.Group
	for i := range kvmHV.Spec.Groups {
		if kvmHV.Spec.Groups[i].Trait == nil {
			nonTraitGroups = append(nonTraitGroups, kvmHV.Spec.Groups[i])
		}
	}
	nonTraitGroups = append(nonTraitGroups,
		hv1.Group{Trait: &hv1.TraitGroup{Name: testTrait1}},
		hv1.Group{Trait: &hv1.TraitGroup{Name: testTrait2}},
	)
	kvmHV.Spec.Groups = nonTraitGroups
	if err := cl.Update(ctx, kvmHV); err != nil {
		return fmt.Errorf("failed to seed spec.groups with test traits: %w", err)
	}
	log.Info("Seeded spec.groups with test traits", "uuid", kvmUUID)

	// Always restore original groups on exit.
	defer func() {
		log.Info("Restoring original spec.groups", "uuid", kvmUUID)
		if err := cl.Get(ctx, client.ObjectKeyFromObject(kvmHV), kvmHV); err != nil {
			log.Error(err, "failed to refetch hypervisor for restoration")
			return
		}
		kvmHV.Spec.Groups = originalGroups
		if err := cl.Update(ctx, kvmHV); err != nil {
			log.Error(err, "failed to restore original spec.groups")
		}
	}()

	// Refetch to get updated generation.
	if err := cl.Get(ctx, client.ObjectKeyFromObject(kvmHV), kvmHV); err != nil {
		return fmt.Errorf("failed to refetch hypervisor after seed: %w", err)
	}

	// Test GET — should return the seeded traits.
	// Poll because the shim's informer cache may take a moment to observe the update.
	log.Info("Testing GET /resource_providers/{uuid}/traits (CRD)", "uuid", kvmUUID)
	var traitsResp struct {
		Traits                     []string `json:"traits"`
		ResourceProviderGeneration int64    `json:"resource_provider_generation"`
	}
	if err := e2ePollUntil(ctx, 10*time.Second, func() (bool, error) {
		req, err := http.NewRequestWithContext(ctx,
			http.MethodGet, sc.Endpoint+"/resource_providers/"+kvmUUID+"/traits", http.NoBody)
		if err != nil {
			return false, err
		}
		req.Header.Set("X-Auth-Token", sc.TokenID)
		req.Header.Set("OpenStack-API-Version", "placement 1.6")
		req.Header.Set("Accept", "application/json")
		resp, err := sc.HTTPClient.Do(req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Errorf("GET CRD traits: expected 200, got %d", resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(&traitsResp); err != nil {
			return false, fmt.Errorf("failed to decode CRD traits response: %w", err)
		}
		return slices.Contains(traitsResp.Traits, testTrait1) &&
			slices.Contains(traitsResp.Traits, testTrait2), nil
	}); err != nil {
		return fmt.Errorf("waiting for seeded traits: %w (got %v)", err, traitsResp.Traits)
	}
	log.Info("Verified GET returns seeded traits from CRD",
		"traits", traitsResp.Traits, "generation", traitsResp.ResourceProviderGeneration)

	// Test PUT — replace traits.
	const replacementTrait = "CUSTOM_E2E_CRD_REPLACED"
	putBody, err := json.Marshal(map[string]any{
		"resource_provider_generation": traitsResp.ResourceProviderGeneration,
		"traits":                       []string{replacementTrait},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/resource_providers/"+kvmUUID+"/traits",
		bytes.NewReader(putBody))
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("PUT CRD traits: expected 200, got %d", resp.StatusCode)
	}
	log.Info("Successfully replaced traits via PUT (CRD)")

	// Test PUT with stale generation — should return 409.
	putBody, err = json.Marshal(map[string]any{
		"resource_provider_generation": traitsResp.ResourceProviderGeneration,
		"traits":                       []string{"STALE"},
	})
	if err != nil {
		return err
	}
	req, err = http.NewRequestWithContext(ctx,
		http.MethodPut, sc.Endpoint+"/resource_providers/"+kvmUUID+"/traits",
		bytes.NewReader(putBody))
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		return fmt.Errorf("PUT CRD traits (stale gen): expected 409, got %d", resp.StatusCode)
	}
	log.Info("Verified generation conflict returns 409")

	// Test GET — verify replacement persisted.
	req, err = http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/resource_providers/"+kvmUUID+"/traits", http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	req.Header.Set("Accept", "application/json")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&traitsResp); err != nil {
		return err
	}
	if len(traitsResp.Traits) != 1 || traitsResp.Traits[0] != replacementTrait {
		return fmt.Errorf("expected [%s], got %v", replacementTrait, traitsResp.Traits)
	}
	log.Info("Verified replacement trait persisted")

	// Test DELETE — remove all traits.
	req, err = http.NewRequestWithContext(ctx,
		http.MethodDelete, sc.Endpoint+"/resource_providers/"+kvmUUID+"/traits", http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	resp, err = sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("DELETE CRD traits: expected 204, got %d", resp.StatusCode)
	}
	log.Info("Verified DELETE returns 204")

	return nil
}

func init() {
	e2eTests = append(e2eTests, e2eTest{name: "resource_provider_traits", run: e2eWrapWithModes(e2eTestResourceProviderTraits)})
}
