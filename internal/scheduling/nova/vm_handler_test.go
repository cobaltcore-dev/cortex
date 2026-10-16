// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	vmopv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func vmTestScheme(t *testing.T) *runtime.Scheme {
	scheme := runtime.NewScheme()
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme hv1: %v", err)
	}
	if err := vmopv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme vmop: %v", err)
	}
	return scheme
}

// vmTestRequest builds a minimal scheduling request targeting compute-1 /
// hv-host-1 with a 4 vCPU, 8 GiB flavor.
func vmTestRequest() api.ExternalSchedulerRequest {
	req := api.ExternalSchedulerRequest{
		Hosts: []api.ExternalSchedulerHost{
			{ComputeHost: "compute-1", HypervisorHostname: "hv-host-1"},
		},
	}
	req.Spec.Data.InstanceUUID = "vm-uuid-1"
	req.Spec.Data.ProjectID = "project-1"
	req.Spec.Data.UserID = "user-1"
	req.Spec.Data.AvailabilityZone = "az-1"
	req.Spec.Data.SchedulerHints = map[string]any{"_nova_check_type": "create"}
	req.Spec.Data.Flavor.Data = api.NovaFlavor{
		Name:     "m1.large",
		FlavorID: "f1",
		VCPUs:    4,
		MemoryMB: 8192,
		ExtraSpecs: map[string]string{
			"capabilities:hypervisor_type": "QEMU",
		},
	}
	req.Spec.Data.Image.Data = api.NovaImageMeta{ID: "image-1"}
	return req
}

func vmTestDecision(targetHost *string) *v1alpha1.Decision {
	return &v1alpha1.Decision{Status: v1alpha1.DecisionStatus{
		Result: &v1alpha1.DecisionResult{TargetHost: targetHost},
	}}
}

func vmTestHypervisor(region, az string) *hv1.Hypervisor {
	labels := map[string]string{}
	if region != "" {
		labels[corev1.LabelTopologyRegion] = region
	}
	if az != "" {
		labels[corev1.LabelTopologyZone] = az
	}
	return &hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "compute-1", Labels: labels}}
}

func TestVirtualMachineHandlerCreate(t *testing.T) {
	scheme := vmTestScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&vmopv1alpha1.VirtualMachine{}).
		WithObjects(vmTestHypervisor("region-1", "az-1")).
		Build()
	h := &VirtualMachineHandler{Client: fakeClient}

	targetHost := "compute-1"
	if err := h.Upsert(context.Background(), vmTestDecision(&targetHost), vmTestRequest()); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	vm := &vmopv1alpha1.VirtualMachine{}
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Name: "vm-uuid-1"}, vm); err != nil {
		t.Fatalf("get created vm: %v", err)
	}
	if vm.Spec.Region != "region-1" || vm.Spec.AZ != "az-1" {
		t.Errorf("region/az = %q/%q, want region-1/az-1", vm.Spec.Region, vm.Spec.AZ)
	}
	if vm.Spec.TargetHost == nil || vm.Spec.TargetHost.Name != "compute-1" {
		t.Errorf("target host ref = %+v, want name compute-1", vm.Spec.TargetHost)
	}
	if cpu := vm.Spec.Resources[vmopv1alpha1.ResourceCPU]; cpu.Value() != 4 {
		t.Errorf("cpu = %d, want 4", cpu.Value())
	}
	if mem := vm.Spec.Resources[vmopv1alpha1.ResourceMemory]; mem.Value() != 8192*1024*1024 {
		t.Errorf("memory = %d, want %d", mem.Value(), 8192*1024*1024)
	}
	if vm.Spec.Nova.Ownership.Project != "project-1" || vm.Spec.Nova.Flavor.Name != "m1.large" {
		t.Errorf("nova spec = %+v, want project-1 / m1.large", vm.Spec.Nova)
	}
	if vm.Labels[vmopv1alpha1.LabelTargetHost] != "compute-1" {
		t.Errorf("target host label = %q, want compute-1", vm.Labels[vmopv1alpha1.LabelTargetHost])
	}
}

func TestVirtualMachineHandlerUpdate(t *testing.T) {
	scheme := vmTestScheme(t)
	existing := &vmopv1alpha1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "vm-uuid-1",
			Labels: map[string]string{vmopv1alpha1.LabelTargetHost: "old-host"},
		},
		Spec: vmopv1alpha1.VMSpec{
			Region:     "region-1",
			AZ:         "az-1",
			TargetHost: &corev1.ObjectReference{Name: "hv-old"},
		},
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&vmopv1alpha1.VirtualMachine{}).
		WithObjects(vmTestHypervisor("region-1", "az-1"), existing).
		Build()
	h := &VirtualMachineHandler{Client: fakeClient}

	targetHost := "compute-1"
	if err := h.Upsert(context.Background(), vmTestDecision(&targetHost), vmTestRequest()); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	vm := &vmopv1alpha1.VirtualMachine{}
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Name: "vm-uuid-1"}, vm); err != nil {
		t.Fatalf("get vm: %v", err)
	}
	if vm.Spec.TargetHost == nil || vm.Spec.TargetHost.Name != "compute-1" {
		t.Errorf("target host ref = %+v, want name compute-1", vm.Spec.TargetHost)
	}
	if vm.Labels[vmopv1alpha1.LabelTargetHost] != "compute-1" {
		t.Errorf("target host label = %q, want compute-1", vm.Labels[vmopv1alpha1.LabelTargetHost])
	}
	// Immutable fields must remain unchanged.
	if vm.Spec.Region != "region-1" || vm.Spec.AZ != "az-1" {
		t.Errorf("immutable region/az changed: %q/%q", vm.Spec.Region, vm.Spec.AZ)
	}
}

func TestVirtualMachineHandlerNoTargetHost(t *testing.T) {
	scheme := vmTestScheme(t)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	h := &VirtualMachineHandler{Client: fakeClient}

	if err := h.Upsert(context.Background(), vmTestDecision(nil), vmTestRequest()); err != nil {
		t.Fatalf("Upsert with nil target host should be a no-op, got: %v", err)
	}
	var vmList vmopv1alpha1.VirtualMachineList
	if err := fakeClient.List(context.Background(), &vmList); err != nil {
		t.Fatalf("list vms: %v", err)
	}
	if len(vmList.Items) != 0 {
		t.Errorf("expected no vms created, got %d", len(vmList.Items))
	}
}

func TestVirtualMachineHandlerMissingRegion(t *testing.T) {
	scheme := vmTestScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(vmTestHypervisor("", "az-1")). // no region label
		Build()
	h := &VirtualMachineHandler{Client: fakeClient}

	targetHost := "compute-1"
	if err := h.Upsert(context.Background(), vmTestDecision(&targetHost), vmTestRequest()); err == nil {
		t.Fatal("expected error for missing region label, got nil")
	}
	vm := &vmopv1alpha1.VirtualMachine{}
	err := fakeClient.Get(context.Background(), client.ObjectKey{Name: "vm-uuid-1"}, vm)
	if !apierrors.IsNotFound(err) {
		t.Errorf("expected no vm created, get returned: %v", err)
	}
}
