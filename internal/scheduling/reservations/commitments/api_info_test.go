// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/sapcc/go-api-declarations/liquid"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHandleInfo_KnowledgeNotReady(t *testing.T) {
	// Test when flavor groups knowledge is not available
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	// No Knowledge CRD created - simulates knowledge not ready
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	api := NewAPI(k8sClient)

	req := httptest.NewRequest(http.MethodGet, "/commitments/v1/info", http.NoBody)
	w := httptest.NewRecorder()

	api.HandleInfo(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	// Should return 503 Service Unavailable when knowledge is not ready
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status code %d (Service Unavailable), got %d", http.StatusServiceUnavailable, resp.StatusCode)
	}

	// Verify Content-Type is text/plain (set by http.Error)
	contentType := resp.Header.Get("Content-Type")
	if contentType != "text/plain; charset=utf-8" {
		t.Errorf("expected Content-Type 'text/plain; charset=utf-8', got %q", contentType)
	}
}

func TestHandleInfo_MethodNotAllowed(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	api := NewAPI(k8sClient)

	// Use POST instead of GET
	req := httptest.NewRequest(http.MethodPost, "/commitments/v1/info", http.NoBody)
	w := httptest.NewRecorder()

	api.HandleInfo(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected status code %d (Method Not Allowed), got %d", http.StatusMethodNotAllowed, resp.StatusCode)
	}
}

func TestHandleInfo_InvalidFlavorMemory(t *testing.T) {
	// Test that a 500 Internal Server Error is returned when a flavor group has invalid data.
	//
	// A flavor with memoryMB=0 is invalid and should trigger an HTTP 500 error.
	// Such data could occur from a bug in the flavor groups extractor.
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	// Create flavor group with memoryMB=0 (invalid data that could come from a buggy extractor)
	features := []map[string]interface{}{
		{
			"name": "invalid_group",
			"flavors": []map[string]interface{}{
				{"name": "zero_memory_flavor", "vcpus": 4, "memoryMB": 0, "diskGB": 50},
			},
			"largestFlavor":  map[string]interface{}{"name": "zero_memory_flavor", "vcpus": 4, "memoryMB": 0, "diskGB": 50},
			"smallestFlavor": map[string]interface{}{"name": "zero_memory_flavor", "vcpus": 4, "memoryMB": 0, "diskGB": 50},
			"ramCoreRatio":   4096,
		},
	}

	raw, err := v1alpha1.BoxFeatureList(features)
	if err != nil {
		t.Fatalf("failed to box features: %v", err)
	}

	knowledge := &v1alpha1.Knowledge{
		ObjectMeta: v1.ObjectMeta{Name: "flavor-groups"},
		Spec: v1alpha1.KnowledgeSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Extractor:        v1alpha1.KnowledgeExtractorSpec{Name: "flavor_groups"},
		},
		Status: v1alpha1.KnowledgeStatus{
			Conditions:        []v1.Condition{{Type: v1alpha1.KnowledgeConditionReady, Status: "True"}},
			Raw:               raw,
			LastContentChange: v1.Now(),
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(knowledge).
		Build()

	api := NewAPI(k8sClient)

	req := httptest.NewRequest(http.MethodGet, "/commitments/v1/info", http.NoBody)
	w := httptest.NewRecorder()
	api.HandleInfo(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	// Should return 500 Internal Server Error when unit creation fails
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected status code %d (Internal Server Error), got %d", http.StatusInternalServerError, resp.StatusCode)
	}
}

func TestHandleInfo_HasCapacityEqualsHandlesCommitments(t *testing.T) {
	// Test that ALL flavor groups get resources created:
	// - Three resources are created per group: _ram, _cores, _instances
	// - Only _ram of groups with FIXED ratio has HandlesCommitments=true
	// - All resources have HasCapacity=true
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	// Create flavor groups knowledge with both fixed and variable ratio groups
	features := []map[string]interface{}{
		{
			// Group with fixed ratio - should accept commitments
			// Creates 3 resources: _ram, _cores, _instances
			"name": "hana_fixed",
			"flavors": []map[string]interface{}{
				{"name": "hana_c4_m16", "vcpus": 4, "memoryMB": 16384, "diskGB": 50},
				{"name": "hana_c8_m32", "vcpus": 8, "memoryMB": 32768, "diskGB": 100},
			},
			"largestFlavor":  map[string]interface{}{"name": "hana_c8_m32", "vcpus": 8, "memoryMB": 32768, "diskGB": 100},
			"smallestFlavor": map[string]interface{}{"name": "hana_c4_m16", "vcpus": 4, "memoryMB": 16384, "diskGB": 50},
			"ramCoreRatio":   4096, // Fixed: 4096 MiB per vCPU for all flavors
		},
		{
			// Group with variable ratio - should NOT accept commitments
			// Will be SKIPPED entirely (no resources created)
			"name": "v2_variable",
			"flavors": []map[string]interface{}{
				{"name": "v2_c4_m8", "vcpus": 4, "memoryMB": 8192, "diskGB": 50},    // 2048 MiB/vCPU
				{"name": "v2_c4_m64", "vcpus": 4, "memoryMB": 65536, "diskGB": 100}, // 16384 MiB/vCPU
			},
			"largestFlavor":   map[string]interface{}{"name": "v2_c4_m64", "vcpus": 4, "memoryMB": 65536, "diskGB": 100},
			"smallestFlavor":  map[string]interface{}{"name": "v2_c4_m8", "vcpus": 4, "memoryMB": 8192, "diskGB": 50},
			"ramCoreRatioMin": 2048,  // Variable: min ratio
			"ramCoreRatioMax": 16384, // Variable: max ratio
		},
	}

	raw, err := v1alpha1.BoxFeatureList(features)
	if err != nil {
		t.Fatalf("failed to box features: %v", err)
	}

	knowledge := &v1alpha1.Knowledge{
		ObjectMeta: v1.ObjectMeta{Name: "flavor-groups"},
		Spec: v1alpha1.KnowledgeSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Extractor:        v1alpha1.KnowledgeExtractorSpec{Name: "flavor_groups"},
		},
		Status: v1alpha1.KnowledgeStatus{
			Conditions:        []v1.Condition{{Type: v1alpha1.KnowledgeConditionReady, Status: "True"}},
			Raw:               raw,
			LastContentChange: v1.Now(),
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(knowledge).
		Build()

	api := NewAPI(k8sClient)

	req := httptest.NewRequest(http.MethodGet, "/commitments/v1/info", http.NoBody)
	w := httptest.NewRecorder()
	api.HandleInfo(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var serviceInfo liquid.ServiceInfo
	if err := json.NewDecoder(resp.Body).Decode(&serviceInfo); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Verify we have 6 resources (3 per flavor group, both groups included)
	// hana_fixed generates: _ram, _cores, _instances
	// v2_variable generates: _ram, _cores, _instances
	if len(serviceInfo.Resources) != 6 {
		t.Fatalf("expected 6 resources (3 per flavor group), got %d", len(serviceInfo.Resources))
	}

	// Test RAM resource: hw_version_hana_fixed_ram
	ramResource, ok := serviceInfo.Resources["hw_version_hana_fixed_ram"]
	if !ok {
		t.Fatal("expected hw_version_hana_fixed_ram resource to exist")
	}
	if !ramResource.HasCapacity {
		t.Error("hw_version_hana_fixed_ram: expected HasCapacity=true")
	}
	if !ramResource.HandlesCommitments {
		t.Error("hw_version_hana_fixed_ram: expected HandlesCommitments=true (RAM is primary commitment resource)")
	}

	// Test Cores resource: hw_version_hana_fixed_cores
	coresResource, ok := serviceInfo.Resources["hw_version_hana_fixed_cores"]
	if !ok {
		t.Fatal("expected hw_version_hana_fixed_cores resource to exist")
	}
	if !coresResource.HasCapacity {
		t.Error("hw_version_hana_fixed_cores: expected HasCapacity=true")
	}
	if coresResource.HandlesCommitments {
		t.Error("hw_version_hana_fixed_cores: expected HandlesCommitments=false (cores are derived)")
	}

	// Test Instances resource: hw_version_hana_fixed_instances
	instancesResource, ok := serviceInfo.Resources["hw_version_hana_fixed_instances"]
	if !ok {
		t.Fatal("expected hw_version_hana_fixed_instances resource to exist")
	}
	if !instancesResource.HasCapacity {
		t.Error("hw_version_hana_fixed_instances: expected HasCapacity=true")
	}
	if instancesResource.HandlesCommitments {
		t.Error("hw_version_hana_fixed_instances: expected HandlesCommitments=false (instances are derived)")
	}

	// Variable ratio group DOES have resources now, but HandlesCommitments=false for RAM
	v2RamResource, ok := serviceInfo.Resources["hw_version_v2_variable_ram"]
	if !ok {
		t.Fatal("expected hw_version_v2_variable_ram resource to exist (all groups included)")
	}
	if !v2RamResource.HasCapacity {
		t.Error("hw_version_v2_variable_ram: expected HasCapacity=true")
	}
	if v2RamResource.HandlesCommitments {
		t.Error("hw_version_v2_variable_ram: expected HandlesCommitments=false (variable ratio)")
	}

	v2CoresResource, ok := serviceInfo.Resources["hw_version_v2_variable_cores"]
	if !ok {
		t.Fatal("expected hw_version_v2_variable_cores resource to exist (all groups included)")
	}
	if !v2CoresResource.HasCapacity {
		t.Error("hw_version_v2_variable_cores: expected HasCapacity=true")
	}
	if v2CoresResource.HandlesCommitments {
		t.Error("hw_version_v2_variable_cores: expected HandlesCommitments=false")
	}

	v2InstancesResource, ok := serviceInfo.Resources["hw_version_v2_variable_instances"]
	if !ok {
		t.Fatal("expected hw_version_v2_variable_instances resource to exist (all groups included)")
	}
	if !v2InstancesResource.HasCapacity {
		t.Error("hw_version_v2_variable_instances: expected HasCapacity=true")
	}
	if v2InstancesResource.HandlesCommitments {
		t.Error("hw_version_v2_variable_instances: expected HandlesCommitments=false")
	}
}
