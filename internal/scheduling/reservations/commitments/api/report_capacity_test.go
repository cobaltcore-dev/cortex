// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/sapcc/go-api-declarations/liquid"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	commitments "github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/commitments"
)

// defaultCapacityConfig enables all three resource types for all groups.
var defaultCapacityConfig = commitments.APIConfig{
	FlavorGroupResourceConfig: map[string]commitments.FlavorGroupResourcesConfig{
		"*": {
			RAM:       commitments.RAMResourceTypeConfig{HasCapacity: true},
			Cores:     commitments.ResourceTypeConfig{HasCapacity: true},
			Instances: commitments.ResourceTypeConfig{HasCapacity: true},
		},
	},
}

func TestHandleReportCapacity(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	api := NewAPIWithConfig(
		fake.NewClientBuilder().WithScheme(scheme).WithObjects(createEmptyFlavorGroupKnowledge()).Build(),
		commitments.APIConfig{EnableReportCapacity: true, FlavorGroupResourceConfig: defaultCapacityConfig.FlavorGroupResourceConfig},
		nil,
	)

	tests := []struct {
		name           string
		method         string
		body           any
		expectedStatus int
	}{
		{name: "POST succeeds", method: http.MethodPost, body: liquid.ServiceCapacityRequest{}, expectedStatus: http.StatusOK},
		{name: "POST with empty body succeeds", method: http.MethodPost, body: nil, expectedStatus: http.StatusOK},
		{name: "GET fails", method: http.MethodGet, body: nil, expectedStatus: http.StatusMethodNotAllowed},
		{name: "PUT fails", method: http.MethodPut, body: nil, expectedStatus: http.StatusMethodNotAllowed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req *http.Request
			if tt.body != nil {
				bodyBytes, err := json.Marshal(tt.body)
				if err != nil {
					t.Fatal(err)
				}
				req = httptest.NewRequest(tt.method, "/commitments/v1/report-capacity", bytes.NewReader(bodyBytes))
			} else {
				req = httptest.NewRequest(tt.method, "/commitments/v1/report-capacity", http.NoBody)
			}
			rr := httptest.NewRecorder()
			api.HandleReportCapacity(rr, req.WithContext(context.Background()))
			if rr.Code != tt.expectedStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.expectedStatus)
			}
		})
	}
}

// TestCapacityCalculator covers calculator behavior across different scenarios.
func TestCapacityCalculator(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	newCalculator := func(objects ...client.Object) *commitments.CapacityCalculator {
		return commitments.NewCapacityCalculator(
			fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
				WithStatusSubresource(&v1alpha1.FlavorGroupCapacity{}).Build(),
			defaultCapacityConfig,
		)
	}

	tests := []struct {
		name    string
		checkFn func(t *testing.T)
	}{
		{
			name: "no knowledge → error",
			checkFn: func(t *testing.T) {
				_, err := newCalculator().CalculateCapacity(context.Background(),
					liquid.ServiceCapacityRequest{AllAZs: []liquid.AvailabilityZone{"az-one"}})
				if err == nil || !strings.Contains(err.Error(), "not found") {
					t.Errorf("expected not-found error, got %v", err)
				}
			},
		},
		{
			name: "empty knowledge → 0 resources",
			checkFn: func(t *testing.T) {
				report, err := newCalculator(createEmptyFlavorGroupKnowledge()).CalculateCapacity(
					context.Background(), liquid.ServiceCapacityRequest{AllAZs: []liquid.AvailabilityZone{"az-one"}})
				if err != nil {
					t.Fatal(err)
				}
				if len(report.Resources) != 0 {
					t.Errorf("expected 0 resources, got %d", len(report.Resources))
				}
			},
		},
		{
			name: "knowledge only → perAZ entries match requested AZs",
			checkFn: func(t *testing.T) {
				azs := []liquid.AvailabilityZone{"qa-de-1a", "qa-de-1b", "qa-de-1d"}
				report, err := newCalculator(createTestFlavorGroupKnowledge(t)).CalculateCapacity(
					context.Background(), liquid.ServiceCapacityRequest{AllAZs: azs})
				if err != nil {
					t.Fatal(err)
				}
				if len(report.Resources) != 3 {
					t.Fatalf("expected 3 resources, got %d", len(report.Resources))
				}
				for _, res := range report.Resources {
					verifyPerAZMatchesRequest(t, res, azs)
				}
			},
		},
		{
			name: "empty AllAZs → empty perAZ maps",
			checkFn: func(t *testing.T) {
				report, err := newCalculator(createTestFlavorGroupKnowledge(t)).CalculateCapacity(
					context.Background(), liquid.ServiceCapacityRequest{AllAZs: []liquid.AvailabilityZone{}})
				if err != nil {
					t.Fatal(err)
				}
				for name, res := range report.Resources {
					if len(res.PerAZ) != 0 {
						t.Errorf("%s: expected empty PerAZ, got %d entries", name, len(res.PerAZ))
					}
				}
			},
		},
		{
			name: "different AZ sets each get their own entries",
			checkFn: func(t *testing.T) {
				calc := newCalculator(createTestFlavorGroupKnowledge(t))
				req1 := liquid.ServiceCapacityRequest{AllAZs: []liquid.AvailabilityZone{"eu-de-1a", "eu-de-1b"}}
				req2 := liquid.ServiceCapacityRequest{AllAZs: []liquid.AvailabilityZone{"us-west-1a", "us-west-1b", "us-west-1c"}}
				for _, req := range []liquid.ServiceCapacityRequest{req1, req2} {
					report, err := calc.CalculateCapacity(context.Background(), req)
					if err != nil {
						t.Fatal(err)
					}
					for _, res := range report.Resources {
						verifyPerAZMatchesRequest(t, res, req.AllAZs)
					}
				}
			},
		},
	}

	// CRD-value cases: all use fixed-ratio knowledge + one CRD for az-one.
	type crdValueCase struct {
		name              string
		totalSlots        int64
		failoverSlots     int64
		ready             bool
		checkAZ           liquid.AvailabilityZone
		wantCapacity      uint64
		wantUsage         *uint64 // nil = expect absent
		cfg               *commitments.APIConfig
		wantResourceCount int  // 0 = don't check
		wantNotReady      bool // expect ErrCapacityNotReady
	}

	crdCases := []crdValueCase{
		{
			// empty-datacenter total=1000 slots, no failover → capacity=1000; usage always None.
			name:       "ready CRD: capacity = empty-datacenter total minus failover, usage None",
			totalSlots: 1000, failoverSlots: 0, ready: true,
			checkAZ: "az-one", wantCapacity: 1000, wantUsage: nil,
		},
		{
			// stale CRD: CalculateCapacity returns ErrCapacityNotReady → caller returns 503
			name:       "stale CRD: returns ErrCapacityNotReady",
			totalSlots: 1000, failoverSlots: 0, ready: false,
			checkAZ: "az-one", wantNotReady: true,
		},
		{
			// CRD only covers az-one; az-two has no CRD → capacity=0
			name:       "missing CRD for AZ: capacity=0",
			totalSlots: 500, failoverSlots: 0, ready: true,
			checkAZ: "az-two", wantCapacity: 0, wantUsage: nil,
		},
	}

	for _, tc := range crdCases {
		tests = append(tests, struct {
			name    string
			checkFn func(t *testing.T)
		}{
			name: tc.name,
			checkFn: func(t *testing.T) {
				cfg := defaultCapacityConfig
				if tc.cfg != nil {
					cfg = *tc.cfg
				}
				crd := createTestFlavorGroupCapacity(tc.totalSlots, tc.failoverSlots, tc.ready)
				calc := commitments.NewCapacityCalculator(
					fake.NewClientBuilder().WithScheme(scheme).
						WithObjects(createTestFlavorGroupKnowledge(t), crd).
						WithStatusSubresource(crd).Build(),
					cfg,
				)
				allAZs := []liquid.AvailabilityZone{"az-one"}
				if tc.checkAZ != "az-one" {
					allAZs = append(allAZs, tc.checkAZ)
				}
				report, err := calc.CalculateCapacity(context.Background(),
					liquid.ServiceCapacityRequest{AllAZs: allAZs})
				if tc.wantNotReady {
					if !errors.Is(err, commitments.ErrCapacityNotReady) {
						t.Fatalf("expected ErrCapacityNotReady, got %v", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if tc.wantResourceCount > 0 && len(report.Resources) != tc.wantResourceCount {
					t.Fatalf("expected %d resources, got %d", tc.wantResourceCount, len(report.Resources))
				}
				ramRes := report.Resources["hw_version_test-group_ram"]
				if ramRes == nil {
					t.Fatal("missing hw_version_test-group_ram")
				}
				az := ramRes.PerAZ[tc.checkAZ]
				if az == nil {
					t.Fatalf("missing entry for AZ %s", tc.checkAZ)
				}
				if az.Capacity != tc.wantCapacity {
					t.Errorf("capacity = %d, want %d", az.Capacity, tc.wantCapacity)
				}
				if tc.wantUsage == nil {
					if az.Usage.IsSome() {
						t.Error("expected usage absent, got value")
					}
				} else {
					if usage := az.Usage.UnwrapOr(99999); usage != *tc.wantUsage {
						t.Errorf("usage = %d, want %d", usage, *tc.wantUsage)
					}
				}
			},
		})
	}

	// HasCapacity=false case — different config, checks resource count.
	tests = append(tests, struct {
		name    string
		checkFn func(t *testing.T)
	}{
		name: "HasCapacity=false omits resource from report",
		checkFn: func(t *testing.T) {
			cfg := commitments.APIConfig{
				FlavorGroupResourceConfig: map[string]commitments.FlavorGroupResourcesConfig{
					"*": {
						RAM:       commitments.RAMResourceTypeConfig{HasCapacity: true},
						Cores:     commitments.ResourceTypeConfig{HasCapacity: true},
						Instances: commitments.ResourceTypeConfig{HasCapacity: false},
					},
				},
			}
			crd := createTestFlavorGroupCapacity(100, 0, true)
			calc := commitments.NewCapacityCalculator(
				fake.NewClientBuilder().WithScheme(scheme).
					WithObjects(createTestFlavorGroupKnowledge(t), crd).
					WithStatusSubresource(crd).Build(),
				cfg,
			)
			report, err := calc.CalculateCapacity(context.Background(),
				liquid.ServiceCapacityRequest{AllAZs: []liquid.AvailabilityZone{"az-one"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Resources) != 2 {
				t.Errorf("expected 2 resources (ram, cores), got %d", len(report.Resources))
			}
			if _, ok := report.Resources["hw_version_test-group_instances"]; ok {
				t.Error("hw_version_test-group_instances should be absent")
			}
		},
	})

	for _, tt := range tests {
		t.Run(tt.name, tt.checkFn)
	}
}

// TestCapacityCalculator_VariableRatio tests RAM capacity/usage for variable-ratio groups.
// Covers both exact-MiB flavors and the 16 MiB vRAM offset case (hw_video:ram_max_mb=16),
// where the actual MemoryMB is 16 less than the nominal value. Both must be handled correctly.
func TestCapacityCalculator_VariableRatio(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	const ramUnitGiB = 2 // 1 declared unit = 2 GiB

	tests := []struct {
		name         string
		flavorMemMiB int
		wantRAMCap   uint64
	}{
		{
			// Exact: 3 running + 5 free = 8 × 2 GiB = 8 declared units.
			name:         "exact 2 GiB flavor (no vRAM offset)",
			flavorMemMiB: 2048,
			wantRAMCap:   8,
		},
		{
			// 3 VMs × 2032 MiB = 6096 MiB. 6096 / 2048 = 2 (not 3) — undercount by 1 unit.
			// Capacity: (3+5)×2032 MiB / 2048 MiB = 7 (not 8). Known limitation.
			name:         "2032 MiB flavor (16 MiB vRAM offset, hw_video:ram_max_mb=16)",
			flavorMemMiB: 2032,
			wantRAMCap:   7,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flavorMemBytes := int64(tc.flavorMemMiB) * 1024 * 1024
			knowledge := createVariableRatioFlavorGroupKnowledge(t, tc.flavorMemMiB)
			// 3 running VMs, 5 exclusively free slots
			crd := createFlavorGroupCapacityWithResources(3, 5*flavorMemBytes, 3*flavorMemBytes, 0)
			cfg := commitments.APIConfig{
				FlavorGroupResourceConfig: map[string]commitments.FlavorGroupResourcesConfig{
					"*": {RAM: commitments.RAMResourceTypeConfig{HasCapacity: true, RAMUnitGiB: ramUnitGiB}},
				},
			}
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).WithObjects(knowledge, crd).WithStatusSubresource(crd).Build()

			report, err := commitments.NewCapacityCalculator(fakeClient, cfg).CalculateCapacity(
				context.Background(), liquid.ServiceCapacityRequest{AllAZs: []liquid.AvailabilityZone{"az-one"}})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			az := report.Resources["hw_version_test-group_ram"].PerAZ["az-one"]
			if az.Capacity != tc.wantRAMCap {
				t.Errorf("RAM capacity = %d, want %d", az.Capacity, tc.wantRAMCap)
			}
			if az.Usage.IsSome() {
				t.Errorf("RAM usage = %v, want None", az.Usage)
			}
		})
	}
}

// TestCapacityCalculator_VariableRatio_PrefersRawCapacity verifies that when
// ExclusivelyRawCapacity is set, it is used for RAM capacity instead of the
// slot-quantized ExclusivelyFreeCapacity fallback.
func TestCapacityCalculator_VariableRatio_PrefersRawCapacity(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	const (
		ramUnitGiB     = 2
		flavorMemMiB   = 2048
		ramUnitBytes   = ramUnitGiB * 1024 * 1024 * 1024
		flavorMemBytes = int64(flavorMemMiB) * 1024 * 1024
	)

	// 3 running VMs, 5 exclusively free slots (slot-quantized fallback = 8 units).
	// Raw hardware = 20 GiB → 10 declared units. If raw is used, capacity = 10, not 8.
	const rawMemBytes = 20 * 1024 * 1024 * 1024

	knowledge := createVariableRatioFlavorGroupKnowledge(t, flavorMemMiB)
	crd := createFlavorGroupCapacityWithResources(3, 5*flavorMemBytes, 3*flavorMemBytes, rawMemBytes)
	cfg := commitments.APIConfig{
		FlavorGroupResourceConfig: map[string]commitments.FlavorGroupResourcesConfig{
			"*": {RAM: commitments.RAMResourceTypeConfig{HasCapacity: true, RAMUnitGiB: ramUnitGiB}},
		},
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).WithObjects(knowledge, crd).WithStatusSubresource(crd).Build()

	report, err := commitments.NewCapacityCalculator(fakeClient, cfg).CalculateCapacity(
		context.Background(), liquid.ServiceCapacityRequest{AllAZs: []liquid.AvailabilityZone{"az-one"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	az := report.Resources["hw_version_test-group_ram"].PerAZ["az-one"]
	if az.Capacity != 10 {
		t.Errorf("RAM capacity = %d, want 10 (raw hardware / ramUnit, not slot-quantized fallback 8)", az.Capacity)
	}
	if az.Usage.IsSome() {
		t.Errorf("RAM usage = %v, want None", az.Usage)
	}
}

// TestCapacityCalculator_FixedRatioIncludesReservedInTotal verifies that for a fixed-ratio group
// the empty-datacenter total already accounts for committed-reserved hosts, so committed slots are
// not added a second time on top of totalSlots. Capacity = totalSlots (no failover); usage None.
func TestCapacityCalculator_FixedRatioIncludesReservedInTotal(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	const flavorMemBytes = 32752 * 1024 * 1024

	// empty-datacenter total=1100 slots, already including 100 committed-reserved slots.
	// capacity must stay 1100 (committed not re-added); usage None.
	crd := createTestFlavorGroupCapacity(1100, 0, true)
	crd.Status.ExclusivelyCommittedReservedSlots = 100
	crd.Status.ExclusivelyCommittedReservedCapacity = map[string]resource.Quantity{
		string(v1alpha1.CommittedResourceTypeMemory): *resource.NewQuantity(100*flavorMemBytes, resource.BinarySI),
		string(v1alpha1.CommittedResourceTypeCores):  *resource.NewQuantity(100*8, resource.DecimalSI),
	}
	calc := commitments.NewCapacityCalculator(
		fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(createTestFlavorGroupKnowledge(t), crd).WithStatusSubresource(crd).Build(),
		defaultCapacityConfig,
	)
	report, err := calc.CalculateCapacity(context.Background(),
		liquid.ServiceCapacityRequest{AllAZs: []liquid.AvailabilityZone{"az-one"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, res := range []string{"hw_version_test-group_ram", "hw_version_test-group_instances"} {
		az := report.Resources[liquid.ResourceName(res)].PerAZ["az-one"]
		if az.Capacity != 1100 {
			t.Errorf("%s: capacity = %d, want 1100 (empty-datacenter total, committed not re-added)", res, az.Capacity)
		}
		if az.Usage.IsSome() {
			t.Errorf("%s: usage = %v, want None", res, az.Usage)
		}
	}
	// cores = empty-datacenter total cores = 1100 * 8 vCPUs.
	cores := report.Resources["hw_version_test-group_cores"].PerAZ["az-one"]
	if cores.Capacity != 1100*8 {
		t.Errorf("cores capacity = %d, want %d (empty-datacenter total cores)", cores.Capacity, 1100*8)
	}
	if cores.Usage.IsSome() {
		t.Errorf("cores usage = %v, want None", cores.Usage)
	}
}

// TestCapacityCalculator_ExcludesFailoverReserved verifies that for a fixed-ratio group the
// failover slice (ExclusivelyFailoverReservedSlots/Capacity) is subtracted from the empty-datacenter
// total, while committed reservations stay included (they are already baked into the total).
// Usage is None throughout.
func TestCapacityCalculator_ExcludesFailoverReserved(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	const flavorMemBytes = 32752 * 1024 * 1024

	// empty-datacenter total=1100 slots (includes 70 committed), failover=30 slots (excluded).
	// instances/ram = 1100 - 30 = 1070.
	crd := createTestFlavorGroupCapacity(1100, 30, true)
	crd.Status.ExclusivelyCommittedReservedSlots = 70
	crd.Status.ExclusivelyCommittedReservedCapacity = map[string]resource.Quantity{
		string(v1alpha1.CommittedResourceTypeMemory): *resource.NewQuantity(70*flavorMemBytes, resource.BinarySI),
		string(v1alpha1.CommittedResourceTypeCores):  *resource.NewQuantity(70*8, resource.DecimalSI),
	}
	calc := commitments.NewCapacityCalculator(
		fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(createTestFlavorGroupKnowledge(t), crd).WithStatusSubresource(crd).Build(),
		defaultCapacityConfig,
	)
	report, err := calc.CalculateCapacity(context.Background(),
		liquid.ServiceCapacityRequest{AllAZs: []liquid.AvailabilityZone{"az-one"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, res := range []string{"hw_version_test-group_ram", "hw_version_test-group_instances"} {
		az := report.Resources[liquid.ResourceName(res)].PerAZ["az-one"]
		if az.Capacity != 1070 {
			t.Errorf("%s: capacity = %d, want 1070 (total 1100 minus failover 30)", res, az.Capacity)
		}
		if az.Usage.IsSome() {
			t.Errorf("%s: usage = %v, want None", res, az.Usage)
		}
	}
	// cores = total cores (1100*8) minus failover cores (30*8) = 8560.
	cores := report.Resources["hw_version_test-group_cores"].PerAZ["az-one"]
	if cores.Capacity != (1100-30)*8 {
		t.Errorf("cores capacity = %d, want %d (total cores minus failover cores)", cores.Capacity, (1100-30)*8)
	}
}

// TestCapacityCalculator_FixedRatioUnresolvableSmallestFlavor verifies that a Ready fixed-ratio
// CRD whose SmallestFlavorName does not match any flavor entry yields ErrCapacityNotReady rather
// than silently reporting zero HANA capacity for the whole AZ.
func TestCapacityCalculator_FixedRatioUnresolvableSmallestFlavor(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	crd := createTestFlavorGroupCapacity(1100, 0, true)
	crd.Status.SmallestFlavorName = "nonexistent_flavor"
	calc := commitments.NewCapacityCalculator(
		fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(createTestFlavorGroupKnowledge(t), crd).WithStatusSubresource(crd).Build(),
		defaultCapacityConfig,
	)
	_, err := calc.CalculateCapacity(context.Background(),
		liquid.ServiceCapacityRequest{AllAZs: []liquid.AvailabilityZone{"az-one"}})
	if !errors.Is(err, commitments.ErrCapacityNotReady) {
		t.Fatalf("expected ErrCapacityNotReady, got %v", err)
	}
}

// TestCapacityCalculator_VariableRatio_RawSubtractsFailover verifies that for a variable-ratio
// group the RAM raw path subtracts only the failover slice: raw already includes reserved-but-empty
// hosts, so committed reserved stays counted while failover is removed.
func TestCapacityCalculator_VariableRatio_RawSubtractsFailover(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	const (
		ramUnitGiB     = 2
		flavorMemMiB   = 2048
		flavorMemBytes = int64(flavorMemMiB) * 1024 * 1024
		rawMemBytes    = 20 * 1024 * 1024 * 1024 // 20 GiB
		failoverBytes  = 4 * 1024 * 1024 * 1024  // 4 GiB failover
	)
	knowledge := createVariableRatioFlavorGroupKnowledge(t, flavorMemMiB)
	crd := createFlavorGroupCapacityWithResources(2, 5*flavorMemBytes, 3*flavorMemBytes, rawMemBytes)
	crd.Status.ExclusivelyFailoverReservedCapacity = map[string]resource.Quantity{
		string(v1alpha1.CommittedResourceTypeMemory): *resource.NewQuantity(failoverBytes, resource.BinarySI),
	}
	cfg := commitments.APIConfig{
		FlavorGroupResourceConfig: map[string]commitments.FlavorGroupResourcesConfig{
			"*": {RAM: commitments.RAMResourceTypeConfig{HasCapacity: true, RAMUnitGiB: ramUnitGiB}},
		},
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).WithObjects(knowledge, crd).WithStatusSubresource(crd).Build()
	report, err := commitments.NewCapacityCalculator(fakeClient, cfg).CalculateCapacity(
		context.Background(), liquid.ServiceCapacityRequest{AllAZs: []liquid.AvailabilityZone{"az-one"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	az := report.Resources["hw_version_test-group_ram"].PerAZ["az-one"]
	// (20 GiB raw - 4 GiB failover) / 2 GiB = 8 units.
	if az.Capacity != 8 {
		t.Errorf("RAM capacity = %d, want 8 ((raw 20 GiB - failover 4 GiB) / 2 GiB)", az.Capacity)
	}
}

// TestCapacityCalculator_VariableRatio_RawIgnoresReserved verifies that for a variable-ratio
// group the RAM raw path already includes committed reserved hardware, so it is NOT added again
// (no double count) when there is no failover to subtract.
func TestCapacityCalculator_VariableRatio_RawIgnoresReserved(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	const (
		ramUnitGiB     = 2
		flavorMemMiB   = 2048
		flavorMemBytes = int64(flavorMemMiB) * 1024 * 1024
		rawMemBytes    = 20 * 1024 * 1024 * 1024 // 20 GiB → 10 declared units
	)
	knowledge := createVariableRatioFlavorGroupKnowledge(t, flavorMemMiB)
	crd := createFlavorGroupCapacityWithResources(3, 5*flavorMemBytes, 3*flavorMemBytes, rawMemBytes)
	// Committed reserved is set but must be ignored on the raw RAM path (raw already includes it).
	crd.Status.ExclusivelyCommittedReservedCapacity = map[string]resource.Quantity{
		string(v1alpha1.CommittedResourceTypeMemory): *resource.NewQuantity(4*1024*1024*1024, resource.BinarySI),
	}
	crd.Status.ExclusivelyCommittedReservedSlots = 2
	cfg := commitments.APIConfig{
		FlavorGroupResourceConfig: map[string]commitments.FlavorGroupResourcesConfig{
			"*": {RAM: commitments.RAMResourceTypeConfig{HasCapacity: true, RAMUnitGiB: ramUnitGiB}},
		},
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).WithObjects(knowledge, crd).WithStatusSubresource(crd).Build()
	report, err := commitments.NewCapacityCalculator(fakeClient, cfg).CalculateCapacity(
		context.Background(), liquid.ServiceCapacityRequest{AllAZs: []liquid.AvailabilityZone{"az-one"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	az := report.Resources["hw_version_test-group_ram"].PerAZ["az-one"]
	if az.Capacity != 10 {
		t.Errorf("RAM capacity = %d, want 10 (raw hardware only, reserved not double counted)", az.Capacity)
	}
}

// TestCapacityCalculator_VariableRatio_CoresPrefersRaw verifies that for a variable-ratio group
// _cores uses raw hardware cores minus failover, consistent with the _ram raw path, rather than the
// running + free + committed sum.
func TestCapacityCalculator_VariableRatio_CoresPrefersRaw(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	const (
		flavorMemMiB   = 2048
		flavorMemBytes = int64(flavorMemMiB) * 1024 * 1024
	)
	knowledge := createVariableRatioFlavorGroupKnowledge(t, flavorMemMiB)
	// Helper sets RunningResources cores to 3*8=24; raw cores (100) minus failover cores (10) = 90,
	// which differs from the summation (24) so a correct result proves the raw path is used.
	crd := createFlavorGroupCapacityWithResources(3, 5*flavorMemBytes, 3*flavorMemBytes, 0)
	crd.Status.ExclusivelyRawCapacity = map[string]resource.Quantity{
		string(v1alpha1.CommittedResourceTypeCores): *resource.NewQuantity(100, resource.DecimalSI),
	}
	crd.Status.ExclusivelyFailoverReservedCapacity = map[string]resource.Quantity{
		string(v1alpha1.CommittedResourceTypeCores): *resource.NewQuantity(10, resource.DecimalSI),
	}
	cfg := commitments.APIConfig{
		FlavorGroupResourceConfig: map[string]commitments.FlavorGroupResourcesConfig{
			"*": {Cores: commitments.ResourceTypeConfig{HasCapacity: true}},
		},
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).WithObjects(knowledge, crd).WithStatusSubresource(crd).Build()
	report, err := commitments.NewCapacityCalculator(fakeClient, cfg).CalculateCapacity(
		context.Background(), liquid.ServiceCapacityRequest{AllAZs: []liquid.AvailabilityZone{"az-one"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cores := report.Resources["hw_version_test-group_cores"].PerAZ["az-one"]
	if cores.Capacity != 90 {
		t.Errorf("cores capacity = %d, want 90 (raw 100 minus failover 10)", cores.Capacity)
	}
	if cores.Usage.IsSome() {
		t.Errorf("cores usage = %v, want None", cores.Usage)
	}
}

func verifyPerAZMatchesRequest(t *testing.T, res *liquid.ResourceCapacityReport, requestedAZs []liquid.AvailabilityZone) {
	t.Helper()
	if res == nil {
		t.Error("resource is nil")
		return
	}
	if len(res.PerAZ) != len(requestedAZs) {
		t.Errorf("expected %d AZs, got %d", len(requestedAZs), len(res.PerAZ))
	}
	for _, az := range requestedAZs {
		if _, ok := res.PerAZ[az]; !ok {
			t.Errorf("missing entry for AZ %s", az)
		}
	}
	for az := range res.PerAZ {
		if !slices.Contains(requestedAZs, az) {
			t.Errorf("unexpected AZ %s in response", az)
		}
	}
}

func createEmptyFlavorGroupKnowledge() *v1alpha1.Knowledge {
	raw, err := v1alpha1.BoxFeatureList([]map[string]interface{}{})
	if err != nil {
		panic(err)
	}
	return &v1alpha1.Knowledge{
		ObjectMeta: v1.ObjectMeta{Name: "flavor-groups"},
		Spec: v1alpha1.KnowledgeSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Extractor:        v1alpha1.KnowledgeExtractorSpec{Name: "flavor_groups"},
		},
		Status: v1alpha1.KnowledgeStatus{
			Conditions: []v1.Condition{{Type: v1alpha1.KnowledgeConditionReady, Status: "True"}},
			Raw:        raw,
		},
	}
}

// createTestFlavorGroupCapacity creates a FlavorGroupCapacity CRD for a fixed-ratio group,
// populated for the empty-datacenter-minus-failover report path: totalSlots is the smallest
// flavor's empty-datacenter slot count and failoverSlots the evacuation carve-out. The test
// flavor has 8 vCPUs, so cores scale as slots*8.
func createTestFlavorGroupCapacity(totalSlots, failoverSlots int64, ready bool) *v1alpha1.FlavorGroupCapacity {
	conditionStatus := v1.ConditionTrue
	if !ready {
		conditionStatus = v1.ConditionFalse
	}
	const vcpus = 8
	status := v1alpha1.FlavorGroupCapacityStatus{
		SmallestFlavorName: "test_c8_m32",
		Flavors: []v1alpha1.FlavorCapacityStatus{
			{FlavorName: "test_c8_m32", TotalCapacityVMSlots: totalSlots},
		},
		TotalCapacity: map[string]resource.Quantity{
			string(v1alpha1.CommittedResourceTypeCores): *resource.NewQuantity(totalSlots*vcpus, resource.DecimalSI),
		},
		Conditions: []v1.Condition{{Type: v1alpha1.FlavorGroupCapacityConditionReady, Status: conditionStatus}},
	}
	if failoverSlots > 0 {
		status.ExclusivelyFailoverReservedSlots = failoverSlots
		status.ExclusivelyFailoverReservedCapacity = map[string]resource.Quantity{
			string(v1alpha1.CommittedResourceTypeCores): *resource.NewQuantity(failoverSlots*vcpus, resource.DecimalSI),
		}
	}
	return &v1alpha1.FlavorGroupCapacity{
		ObjectMeta: v1.ObjectMeta{Name: "test-group-az-one"},
		Spec:       v1alpha1.FlavorGroupCapacitySpec{FlavorGroup: "test-group", AvailabilityZone: "az-one"},
		Status:     status,
	}
}

// createTestFlavorGroupKnowledge creates a fixed-ratio (HANA-style) flavor group Knowledge CRD.
func createTestFlavorGroupKnowledge(t *testing.T) *v1alpha1.Knowledge {
	t.Helper()
	features := []map[string]interface{}{
		{
			"name": "test-group",
			"flavors": []map[string]interface{}{
				{"name": "test_c8_m32", "vcpus": 8, "memoryMB": 32752, "diskGB": 50},
			},
			"largestFlavor":  map[string]interface{}{"name": "test_c8_m32", "vcpus": 8, "memoryMB": 32752, "diskGB": 50},
			"smallestFlavor": map[string]interface{}{"name": "test_c8_m32", "vcpus": 8, "memoryMB": 32752, "diskGB": 50},
			"ramCoreRatio":   4096, // fixed RAM/core ratio → slots-based reporting
		},
	}
	raw, err := v1alpha1.BoxFeatureList(features)
	if err != nil {
		t.Fatal(err)
	}
	return &v1alpha1.Knowledge{
		ObjectMeta: v1.ObjectMeta{Name: "flavor-groups"},
		Spec: v1alpha1.KnowledgeSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Extractor:        v1alpha1.KnowledgeExtractorSpec{Name: "flavor_groups"},
		},
		Status: v1alpha1.KnowledgeStatus{
			Conditions: []v1.Condition{{Type: v1alpha1.KnowledgeConditionReady, Status: "True"}},
			Raw:        raw,
		},
	}
}

// createVariableRatioFlavorGroupKnowledge creates a Knowledge CRD for a variable-ratio group
// (no ramCoreRatio, so RAM is reported in GiB units, not slots).
func createVariableRatioFlavorGroupKnowledge(t *testing.T, flavorMemMiB int) *v1alpha1.Knowledge {
	t.Helper()
	features := []map[string]interface{}{
		{
			"name":           "test-group",
			"flavors":        []map[string]interface{}{{"name": "test-flavor", "vcpus": 8, "memoryMB": flavorMemMiB, "diskGB": 50}},
			"largestFlavor":  map[string]interface{}{"name": "test-flavor", "vcpus": 8, "memoryMB": flavorMemMiB, "diskGB": 50},
			"smallestFlavor": map[string]interface{}{"name": "test-flavor", "vcpus": 8, "memoryMB": flavorMemMiB, "diskGB": 50},
			// no ramCoreRatio → variable-ratio group
		},
	}
	raw, err := v1alpha1.BoxFeatureList(features)
	if err != nil {
		t.Fatal(err)
	}
	return &v1alpha1.Knowledge{
		ObjectMeta: v1.ObjectMeta{Name: "flavor-groups"},
		Spec: v1alpha1.KnowledgeSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Extractor:        v1alpha1.KnowledgeExtractorSpec{Name: "flavor_groups"},
		},
		Status: v1alpha1.KnowledgeStatus{
			Conditions: []v1.Condition{{Type: v1alpha1.KnowledgeConditionReady, Status: "True", Reason: "ExtractorSucceeded"}},
			Raw:        raw,
		},
	}
}

// createFlavorGroupCapacityWithResources creates a ready FlavorGroupCapacity CRD with
// RunningInstances, ExclusivelyFreeCapacity, RunningResources, and optionally
// ExclusivelyRawCapacity all populated. Running cores follow the 8-vCPU test flavor.
func createFlavorGroupCapacityWithResources(runningInstances, exclusiveFreeMemBytes, runningMemBytes, exclusivelyRawMemBytes int64) *v1alpha1.FlavorGroupCapacity {
	status := v1alpha1.FlavorGroupCapacityStatus{
		RunningInstances: runningInstances,
		// Homogeneous single-flavor test group: every running VM is exactly one smallest-flavor slot.
		RunningSlots: runningInstances,
		RunningResources: map[string]resource.Quantity{
			string(v1alpha1.CommittedResourceTypeMemory): *resource.NewQuantity(runningMemBytes, resource.BinarySI),
			string(v1alpha1.CommittedResourceTypeCores):  *resource.NewQuantity(runningInstances*8, resource.DecimalSI),
		},
		ExclusivelyFreeCapacity: map[string]resource.Quantity{
			string(v1alpha1.CommittedResourceTypeMemory): *resource.NewQuantity(exclusiveFreeMemBytes, resource.BinarySI),
		},
		Conditions: []v1.Condition{{Type: v1alpha1.FlavorGroupCapacityConditionReady, Status: v1.ConditionTrue}},
	}
	if exclusivelyRawMemBytes > 0 {
		status.ExclusivelyRawCapacity = map[string]resource.Quantity{
			string(v1alpha1.CommittedResourceTypeMemory): *resource.NewQuantity(exclusivelyRawMemBytes, resource.BinarySI),
		}
	}
	return &v1alpha1.FlavorGroupCapacity{
		ObjectMeta: v1.ObjectMeta{Name: "test-group-az-one"},
		Spec:       v1alpha1.FlavorGroupCapacitySpec{FlavorGroup: "test-group", AvailabilityZone: "az-one"},
		Status:     status,
	}
}
