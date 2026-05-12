// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	commitments "github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/commitments"
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
	// Test that the info endpoint succeeds even when a flavor group has memoryMB=0.
	// With the fixed GiB unit, we no longer reject zero-memory flavors at the info level;
	// they result in zero capacity at the capacity reporting level instead.
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	// Create flavor group with memoryMB=0 (edge case from a buggy extractor)
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

	// Should return 200 OK — zero-memory flavor no longer causes an error
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status code %d (OK), got %d", http.StatusOK, resp.StatusCode)
	}
}

func TestHandleInfo_ResourceFlagsFromConfig(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	// Create flavor groups knowledge with both fixed and variable ratio groups
	features := []map[string]interface{}{
		{
			"name": "hana_fixed",
			"flavors": []map[string]interface{}{
				{"name": "hana_c4_m16", "vcpus": 4, "memoryMB": 16384, "diskGB": 50},
				{"name": "hana_c8_m32", "vcpus": 8, "memoryMB": 32768, "diskGB": 100},
			},
			"largestFlavor":  map[string]interface{}{"name": "hana_c8_m32", "vcpus": 8, "memoryMB": 32768, "diskGB": 100},
			"smallestFlavor": map[string]interface{}{"name": "hana_c4_m16", "vcpus": 4, "memoryMB": 16384, "diskGB": 50},
			// 4094 MiB/vCPU simulates real flavor RAM (4096 MiB nominal − 2 MiB video RAM).
			// Truncating division gives 3; rounding gives 4.
			"ramCoreRatio": 4094,
		},
		{
			"name": "v2_variable",
			"flavors": []map[string]interface{}{
				{"name": "v2_c4_m8", "vcpus": 4, "memoryMB": 8192, "diskGB": 50},
				{"name": "v2_c4_m64", "vcpus": 4, "memoryMB": 65536, "diskGB": 100},
			},
			"largestFlavor":   map[string]interface{}{"name": "v2_c4_m64", "vcpus": 4, "memoryMB": 65536, "diskGB": 100},
			"smallestFlavor":  map[string]interface{}{"name": "v2_c4_m8", "vcpus": 4, "memoryMB": 8192, "diskGB": 50},
			"ramCoreRatioMin": 2048,
			"ramCoreRatioMax": 16384,
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

	// hana_fixed: ram accepts commitments and has quota; v2_variable: nothing accepts commitments
	cfg := commitments.DefaultAPIConfig()
	cfg.FlavorGroupResourceConfig = map[string]commitments.FlavorGroupResourcesConfig{
		"hana_fixed": {
			RAM:       commitments.ResourceTypeConfig{HandlesCommitments: true, HasCapacity: true, HasQuota: true},
			Cores:     commitments.ResourceTypeConfig{HasCapacity: true},
			Instances: commitments.ResourceTypeConfig{HasCapacity: true},
		},
		"*": {
			RAM:       commitments.ResourceTypeConfig{HasCapacity: true},
			Cores:     commitments.ResourceTypeConfig{HasCapacity: true},
			Instances: commitments.ResourceTypeConfig{HasCapacity: true},
		},
	}
	api := NewAPIWithConfig(k8sClient, cfg, nil)

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

	if len(serviceInfo.Resources) != 6 {
		t.Fatalf("expected 6 resources (3 per flavor group), got %d", len(serviceInfo.Resources))
	}

	// Test RAM resource: hw_version_hana_fixed_ram (fixed ratio → commitments + quota)
	ramResource, ok := serviceInfo.Resources["hw_version_hana_fixed_ram"]
	if !ok {
		t.Fatal("expected hw_version_hana_fixed_ram resource to exist")
	}
	if !ramResource.HasCapacity {
		t.Error("hw_version_hana_fixed_ram: expected HasCapacity=true")
	}
	if !ramResource.HandlesCommitments {
		t.Error("hw_version_hana_fixed_ram: expected HandlesCommitments=true (set in config)")
	}
	if ramResource.Topology != liquid.AZSeparatedTopology {
		t.Errorf("hw_version_hana_fixed_ram: expected Topology=%q, got %q", liquid.AZSeparatedTopology, ramResource.Topology)
	}
	if !ramResource.HasQuota {
		t.Error("hw_version_hana_fixed_ram: expected HasQuota=true (fixed ratio groups accept quotas)")
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
		t.Error("hw_version_hana_fixed_cores: expected HandlesCommitments=false")
	}
	if coresResource.Topology != liquid.AZSeparatedTopology {
		t.Errorf("hw_version_hana_fixed_cores: expected Topology=%q, got %q", liquid.AZSeparatedTopology, coresResource.Topology)
	}
	if coresResource.HasQuota {
		t.Error("hw_version_hana_fixed_cores: expected HasQuota=false")
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
		t.Error("hw_version_hana_fixed_instances: expected HandlesCommitments=false")
	}
	if instancesResource.Topology != liquid.AZSeparatedTopology {
		t.Errorf("hw_version_hana_fixed_instances: expected Topology=%q, got %q", liquid.AZSeparatedTopology, instancesResource.Topology)
	}
	if instancesResource.HasQuota {
		t.Error("hw_version_hana_fixed_instances: expected HasQuota=false")
	}

	// v2_variable is covered by "*" wildcard: HasCapacity=true, HandlesCommitments=false
	v2RamResource, ok := serviceInfo.Resources["hw_version_v2_variable_ram"]
	if !ok {
		t.Fatal("expected hw_version_v2_variable_ram resource to exist")
	}
	if !v2RamResource.HasCapacity {
		t.Error("hw_version_v2_variable_ram: expected HasCapacity=true")
	}
	if v2RamResource.HandlesCommitments {
		t.Error("hw_version_v2_variable_ram: expected HandlesCommitments=false (not in config)")
	}
	if v2RamResource.Topology != liquid.AZSeparatedTopology {
		t.Errorf("hw_version_v2_variable_ram: expected Topology=%q, got %q", liquid.AZSeparatedTopology, v2RamResource.Topology)
	}
	if v2RamResource.HasQuota {
		t.Error("hw_version_v2_variable_ram: expected HasQuota=false (variable ratio)")
	}

	v2CoresResource, ok := serviceInfo.Resources["hw_version_v2_variable_cores"]
	if !ok {
		t.Fatal("expected hw_version_v2_variable_cores resource to exist")
	}
	if !v2CoresResource.HasCapacity {
		t.Error("hw_version_v2_variable_cores: expected HasCapacity=true")
	}
	if v2CoresResource.HandlesCommitments {
		t.Error("hw_version_v2_variable_cores: expected HandlesCommitments=false")
	}
	if v2CoresResource.Topology != liquid.AZSeparatedTopology {
		t.Errorf("hw_version_v2_variable_cores: expected Topology=%q, got %q", liquid.AZSeparatedTopology, v2CoresResource.Topology)
	}
	if v2CoresResource.HasQuota {
		t.Error("hw_version_v2_variable_cores: expected HasQuota=false")
	}

	v2InstancesResource, ok := serviceInfo.Resources["hw_version_v2_variable_instances"]
	if !ok {
		t.Fatal("expected hw_version_v2_variable_instances resource to exist")
	}
	if !v2InstancesResource.HasCapacity {
		t.Error("hw_version_v2_variable_instances: expected HasCapacity=true")
	}
	if v2InstancesResource.HandlesCommitments {
		t.Error("hw_version_v2_variable_instances: expected HandlesCommitments=false")
	}
	if v2InstancesResource.Topology != liquid.AZSeparatedTopology {
		t.Errorf("hw_version_v2_variable_instances: expected Topology=%q, got %q", liquid.AZSeparatedTopology, v2InstancesResource.Topology)
	}
	if v2InstancesResource.HasQuota {
		t.Error("hw_version_v2_variable_instances: expected HasQuota=false")
	}

	// Verify ratio attributes are converted from MiB to GiB.
	// hana_fixed has ramCoreRatio=4096 MiB/vCPU → expect 4 GiB/vCPU.
	checkAttrsRatio(t, "hw_version_hana_fixed_ram", ramResource.Attributes, 4, nil, nil)
	// v2_variable has ramCoreRatioMin=2048 MiB/vCPU, ramCoreRatioMax=16384 MiB/vCPU → expect 2, 16 GiB/vCPU.
	checkAttrsRatio(t, "hw_version_v2_variable_ram", v2RamResource.Attributes, 0, ptr(uint64(2)), ptr(uint64(16)))

	// Verify RAM units: fixed-ratio groups use smallest-flavor-based unit (e.g. "16 GiB"),
	// variable-ratio groups use UnitGibibytes ("GiB").
	expectedFixedUnit, err := liquid.UnitMebibytes.MultiplyBy(16384) // hana_fixed smallest flavor: 16384 MiB = 16 GiB
	if err != nil {
		t.Fatalf("failed to create expected fixed unit: %v", err)
	}
	if ramResource.Unit != expectedFixedUnit {
		t.Errorf("hw_version_hana_fixed_ram: expected Unit=%q (slot-based), got %q", expectedFixedUnit, ramResource.Unit)
	}
	if v2RamResource.Unit != liquid.UnitGibibytes {
		t.Errorf("hw_version_v2_variable_ram: expected Unit=%q, got %q", liquid.UnitGibibytes, v2RamResource.Unit)
	}
}

func TestHandleInfo_AllResourcesAZSeparated(t *testing.T) {
	// Verifies that all resources always get AZSeparatedTopology.
	// regardless of whether it's RAM, Cores, or Instances.
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	features := []map[string]interface{}{
		{
			"name":           "fg",
			"flavors":        []map[string]interface{}{{"name": "fg_c4_m16", "vcpus": 4, "memoryMB": 16384, "diskGB": 50}},
			"largestFlavor":  map[string]interface{}{"name": "fg_c4_m16", "vcpus": 4, "memoryMB": 16384, "diskGB": 50},
			"smallestFlavor": map[string]interface{}{"name": "fg_c4_m16", "vcpus": 4, "memoryMB": 16384, "diskGB": 50},
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

	// All three resource types handle commitments.
	cfg := commitments.DefaultAPIConfig()
	cfg.FlavorGroupResourceConfig = map[string]commitments.FlavorGroupResourcesConfig{
		"fg": {
			RAM:       commitments.ResourceTypeConfig{HandlesCommitments: true, HasCapacity: true, HasQuota: true},
			Cores:     commitments.ResourceTypeConfig{HandlesCommitments: true, HasCapacity: true, HasQuota: true},
			Instances: commitments.ResourceTypeConfig{HandlesCommitments: true, HasCapacity: true, HasQuota: true},
		},
	}

	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(knowledge).Build()
	api := NewAPIWithConfig(k8sClient, cfg, nil)

	req := httptest.NewRequest(http.MethodGet, "/commitments/v1/info", http.NoBody)
	w := httptest.NewRecorder()
	api.HandleInfo(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var serviceInfo liquid.ServiceInfo
	if err := json.NewDecoder(resp.Body).Decode(&serviceInfo); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	for _, resName := range []string{"hw_version_fg_ram", "hw_version_fg_cores", "hw_version_fg_instances"} {
		res, ok := serviceInfo.Resources[liquid.ResourceName(resName)]
		if !ok {
			t.Fatalf("expected resource %s", resName)
		}
		if res.Topology != liquid.AZSeparatedTopology {
			t.Errorf("%s: expected Topology=%q (HandlesCommitments=true), got %q", resName, liquid.AZSeparatedTopology, res.Topology)
		}
	}
}

// ptr returns a pointer to v, for use in test assertions.
func ptr[T any](v T) *T { return &v }

// checkAttrsRatio decodes the Attributes JSON of a resource and verifies the ratio fields
// are in GiB/vCPU. Pass ratioGiB=0 to skip the fixed-ratio check; pass nil min/max to skip range checks.
func checkAttrsRatio(t *testing.T, resName string, raw json.RawMessage, ratioGiB uint64, minGiB, maxGiB *uint64) {
	t.Helper()
	var attrs struct {
		RamCoreRatio    *uint64 `json:"ramCoreRatio"`
		RamCoreRatioMin *uint64 `json:"ramCoreRatioMin"`
		RamCoreRatioMax *uint64 `json:"ramCoreRatioMax"`
	}
	if err := json.Unmarshal(raw, &attrs); err != nil {
		t.Fatalf("%s: failed to decode attributes: %v", resName, err)
	}
	if ratioGiB != 0 {
		if attrs.RamCoreRatio == nil {
			t.Errorf("%s: expected ramCoreRatio=%d GiB/vCPU, got nil", resName, ratioGiB)
		} else if *attrs.RamCoreRatio != ratioGiB {
			t.Errorf("%s: expected ramCoreRatio=%d GiB/vCPU, got %d", resName, ratioGiB, *attrs.RamCoreRatio)
		}
	}
	if minGiB != nil {
		if attrs.RamCoreRatioMin == nil {
			t.Errorf("%s: expected ramCoreRatioMin=%d GiB/vCPU, got nil", resName, *minGiB)
		} else if *attrs.RamCoreRatioMin != *minGiB {
			t.Errorf("%s: expected ramCoreRatioMin=%d GiB/vCPU, got %d", resName, *minGiB, *attrs.RamCoreRatioMin)
		}
	}
	if maxGiB != nil {
		if attrs.RamCoreRatioMax == nil {
			t.Errorf("%s: expected ramCoreRatioMax=%d GiB/vCPU, got nil", resName, *maxGiB)
		} else if *attrs.RamCoreRatioMax != *maxGiB {
			t.Errorf("%s: expected ramCoreRatioMax=%d GiB/vCPU, got %d", resName, *maxGiB, *attrs.RamCoreRatioMax)
		}
	}
}
