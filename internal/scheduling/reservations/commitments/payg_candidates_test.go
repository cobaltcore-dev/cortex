// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"errors"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// ============================================================================
// Fake VMSource
// ============================================================================

type fakeVMSource struct {
	vms []reservations.VM
	err error
}

func (f *fakeVMSource) ListVMs(_ context.Context) ([]reservations.VM, error) {
	return f.vms, f.err
}
func (f *fakeVMSource) ListVMsByProject(_ context.Context, _ string) ([]reservations.VM, error) {
	return f.vms, f.err
}
func (f *fakeVMSource) ListVMsOnHypervisors(_ context.Context, _ *hv1.HypervisorList, _ bool) ([]reservations.VM, error) {
	return f.vms, f.err
}
func (f *fakeVMSource) GetVM(_ context.Context, _ string) (*reservations.VM, error) {
	return nil, nil
}
func (f *fakeVMSource) IsServerActive(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (f *fakeVMSource) GetDeletedVMInfo(_ context.Context, _ string) (*reservations.DeletedVMInfo, error) {
	return nil, nil
}

// ============================================================================
// Helpers
// ============================================================================

func paygScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add v1alpha1 scheme: %v", err)
	}
	if err := hv1.AddToScheme(s); err != nil {
		t.Fatalf("add hv1 scheme: %v", err)
	}
	return s
}

func hvWithInstances(name, az string, instances ...hv1.Instance) *hv1.Hypervisor { //nolint:unparam
	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{"topology.kubernetes.io/zone": az},
		},
		Status: hv1.HypervisorStatus{Instances: instances},
	}
}

func activeInstance(id string) hv1.Instance {
	return hv1.Instance{ID: id, Name: id, Active: true}
}

func inactiveInstance(id string) hv1.Instance {
	return hv1.Instance{ID: id, Name: id, Active: false}
}

func vmOnHV(uuid, hvName, flavorName string, memMB uint64) reservations.VM { //nolint:unparam
	return reservations.VM{
		UUID:              uuid,
		FlavorName:        flavorName,
		CurrentHypervisor: hvName,
		Resources: map[string]resource.Quantity{
			"memory": *resource.NewQuantity(int64(memMB)*1024*1024, resource.BinarySI), //nolint:gosec
		},
	}
}

func reservationWithAlloc(name, vmUUID string) *v1alpha1.Reservation {
	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ReservationSpec{
			Type: v1alpha1.ReservationTypeCommittedResource,
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				CommitmentUUID: "other-cr",
				Allocations: map[string]v1alpha1.CommittedResourceAllocation{
					vmUUID: {CreationTimestamp: metav1.Now()},
				},
			},
		},
	}
}

func buildPaygClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(paygScheme(t)).
		WithObjects(objs...).
		WithIndex(&v1alpha1.Reservation{}, idxReservationByAllocationVMUUID, func(obj client.Object) []string {
			res, ok := obj.(*v1alpha1.Reservation)
			if !ok || res.Spec.CommittedResourceReservation == nil {
				return nil
			}
			var uuids []string
			for vmUUID := range res.Spec.CommittedResourceReservation.Allocations {
				uuids = append(uuids, vmUUID)
			}
			return uuids
		}).
		Build()
}

// ============================================================================
// Tests: filterPaygCandidates
// ============================================================================

func TestFilterPaygCandidates(t *testing.T) {
	fg := testFlavorGroup() // flavors: large=32GiB, medium=16GiB, small=8GiB

	tests := []struct {
		name      string
		hv        *hv1.Hypervisor
		vms       []reservations.VM
		extraObjs []client.Object // additional k8s objects (e.g. reservations with allocs)
		wantVMIDs []string        // expected candidate UUIDs (order independent)
		wantCount int
	}{
		{
			name:      "VM matches project and flavor group, not allocated — included",
			hv:        hvWithInstances("host-1", "az-1", activeInstance("vm-a")),
			vms:       []reservations.VM{vmOnHV("vm-a", "host-1", "small", 8192)},
			wantVMIDs: []string{"vm-a"},
		},
		{
			name:      "VM already in Reservation.Spec.Allocations — excluded",
			hv:        hvWithInstances("host-1", "az-1", activeInstance("vm-a")),
			vms:       []reservations.VM{vmOnHV("vm-a", "host-1", "small", 8192)},
			extraObjs: []client.Object{reservationWithAlloc("res-1", "vm-a")},
			wantCount: 0,
		},
		{
			name:      "VM flavor not in flavor group — excluded",
			hv:        hvWithInstances("host-1", "az-1", activeInstance("vm-a")),
			vms:       []reservations.VM{vmOnHV("vm-a", "host-1", "unknown-flavor", 8192)},
			wantCount: 0,
		},
		{
			name:      "VM not active in HV CRD Status.Instances — excluded",
			hv:        hvWithInstances("host-1", "az-1", inactiveInstance("vm-a")),
			vms:       []reservations.VM{vmOnHV("vm-a", "host-1", "small", 8192)},
			wantCount: 0,
		},
		{
			name:      "VM not present in HV CRD instances at all — excluded",
			hv:        hvWithInstances("host-1", "az-1"), // no instances
			vms:       []reservations.VM{vmOnHV("vm-a", "host-1", "small", 8192)},
			wantCount: 0,
		},
		{
			name:      "empty VM list — returns empty, no error",
			hv:        hvWithInstances("host-1", "az-1", activeInstance("vm-a")),
			vms:       nil,
			wantCount: 0,
		},
		{
			name: "multiple VMs — unallocated included, allocated excluded, sorted descending by memory",
			hv: hvWithInstances("host-1", "az-1",
				activeInstance("vm-small"),
				activeInstance("vm-medium"),
				activeInstance("vm-large"),
				activeInstance("vm-allocated"),
			),
			vms: []reservations.VM{
				vmOnHV("vm-small", "host-1", "small", 8192),
				vmOnHV("vm-medium", "host-1", "medium", 16384),
				vmOnHV("vm-large", "host-1", "large", 32768),
				vmOnHV("vm-allocated", "host-1", "small", 8192),
			},
			extraObjs: []client.Object{reservationWithAlloc("res-1", "vm-allocated")},
			wantVMIDs: []string{"vm-large", "vm-medium", "vm-small"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			objs := []client.Object{tc.hv}
			objs = append(objs, tc.extraObjs...)
			k8sClient := buildPaygClient(t, objs...)

			candidates, err := filterPaygCandidates(context.Background(), k8sClient, tc.hv.Name, tc.hv, tc.vms, fg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.wantVMIDs != nil {
				if len(candidates) != len(tc.wantVMIDs) {
					t.Fatalf("want %d candidates, got %d: %v", len(tc.wantVMIDs), len(candidates), candidates)
				}
				for i, want := range tc.wantVMIDs {
					if candidates[i].VMID != want {
						t.Errorf("candidates[%d]: want VMID %q, got %q", i, want, candidates[i].VMID)
					}
				}
			} else if len(candidates) != tc.wantCount {
				t.Errorf("want %d candidates, got %d", tc.wantCount, len(candidates))
			}
		})
	}
}

// ============================================================================
// Tests: ScanAZForPaygCandidates
// ============================================================================

func TestScanAZForPaygCandidates(t *testing.T) {
	fg := testFlavorGroup()

	tests := []struct {
		name        string
		hvs         []*hv1.Hypervisor
		vmSourceVMs []reservations.VM
		vmSourceErr error
		az          string
		projectID   string
		wantHVs     []string // HV names expected in result (non-empty candidate lists)
		wantErr     bool
	}{
		{
			name:        "HV in AZ with matching PAYG VM — returned",
			hvs:         []*hv1.Hypervisor{hvWithInstances("host-1", "az-1", activeInstance("vm-a"))},
			vmSourceVMs: []reservations.VM{vmOnHV("vm-a", "host-1", "small", 8192)},
			az:          "az-1",
			projectID:   "project-1",
			wantHVs:     []string{"host-1"},
		},
		{
			name:        "HV in different AZ — not returned",
			hvs:         []*hv1.Hypervisor{hvWithInstances("host-1", "az-2", activeInstance("vm-a"))},
			vmSourceVMs: []reservations.VM{vmOnHV("vm-a", "host-1", "small", 8192)},
			az:          "az-1",
			projectID:   "project-1",
			wantHVs:     nil,
		},
		{
			name:        "VMSource returns error — propagated",
			hvs:         []*hv1.Hypervisor{hvWithInstances("host-1", "az-1", activeInstance("vm-a"))},
			vmSourceErr: errors.New("db error"),
			az:          "az-1",
			projectID:   "project-1",
			wantErr:     true,
		},
		{
			name:      "no HVs in AZ — returns nil, no error",
			hvs:       []*hv1.Hypervisor{},
			az:        "az-1",
			projectID: "project-1",
			wantHVs:   nil,
		},
		{
			name:        "VMSource returns no VMs — returns empty, no error",
			hvs:         []*hv1.Hypervisor{hvWithInstances("host-1", "az-1", activeInstance("vm-a"))},
			vmSourceVMs: nil,
			az:          "az-1",
			projectID:   "project-1",
			wantHVs:     nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			objs := make([]client.Object, len(tc.hvs))
			for i, hv := range tc.hvs {
				objs[i] = hv
			}
			k8sClient := buildPaygClient(t, objs...)
			vmSource := &fakeVMSource{vms: tc.vmSourceVMs, err: tc.vmSourceErr}

			result, err := ScanAZForPaygCandidates(context.Background(), k8sClient, vmSource, tc.az, tc.projectID, fg)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, hvName := range tc.wantHVs {
				if _, ok := result[hvName]; !ok {
					t.Errorf("expected candidates for HV %q, not present in result", hvName)
				}
			}
			// No unexpected HVs.
			for hvName := range result {
				found := false
				for _, want := range tc.wantHVs {
					if want == hvName {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("unexpected HV %q in result", hvName)
				}
			}
		})
	}
}
