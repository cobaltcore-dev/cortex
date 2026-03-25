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
	// Test that HasCapacity == HandlesCommitments for all resources
	// Both should be true only for groups with fixed RAM/core ratio
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	// Create flavor groups knowledge with both fixed and variable ratio groups
	features := []map[string]interface{}{
		{
			// Group with fixed ratio - should accept commitments (HasCapacity=true, HandlesCommitments=true)
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
			// Group with variable ratio - should NOT accept commitments (HasCapacity=false, HandlesCommitments=false)
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

	// Verify we have both resources
	if len(serviceInfo.Resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(serviceInfo.Resources))
	}

	// Test fixed ratio group: hw_version_hana_fixed_ram
	fixedResource, ok := serviceInfo.Resources["hw_version_hana_fixed_ram"]
	if !ok {
		t.Fatal("expected hw_version_hana_fixed_ram resource to exist")
	}
	if !fixedResource.HasCapacity {
		t.Error("hw_version_hana_fixed_ram: expected HasCapacity=true")
	}
	if !fixedResource.HandlesCommitments {
		t.Error("hw_version_hana_fixed_ram: expected HandlesCommitments=true (fixed ratio group)")
	}
	if fixedResource.HasCapacity != fixedResource.HandlesCommitments {
		t.Errorf("hw_version_hana_fixed_ram: HasCapacity (%v) should equal HandlesCommitments (%v)",
			fixedResource.HasCapacity, fixedResource.HandlesCommitments)
	}

	// Test variable ratio group: hw_version_v2_variable_ram
	variableResource, ok := serviceInfo.Resources["hw_version_v2_variable_ram"]
	if !ok {
		t.Fatal("expected hw_version_v2_variable_ram resource to exist")
	}
	// Variable ratio groups don't accept commitments, and we only report capacity for groups
	// that accept commitments, so both HasCapacity and HandlesCommitments should be false
	if variableResource.HasCapacity {
		t.Error("hw_version_v2_variable_ram: expected HasCapacity=false (variable ratio groups don't report capacity)")
	}
	if variableResource.HandlesCommitments {
		t.Error("hw_version_v2_variable_ram: expected HandlesCommitments=false (variable ratio group)")
	}
	// Verify HasCapacity == HandlesCommitments for consistency
	if variableResource.HasCapacity != variableResource.HandlesCommitments {
		t.Errorf("hw_version_v2_variable_ram: HasCapacity (%v) should equal HandlesCommitments (%v)",
			variableResource.HasCapacity, variableResource.HandlesCommitments)
	}
}
