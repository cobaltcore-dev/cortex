// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	vmv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// newTestClient builds a fake controller-runtime client with the schemes the
// failover host controller needs (reservations, hypervisors, virtual machines)
// and the Reservation status subresource registered. Shared by the Reconcile
// tests here and the loop test in loop_test.go.
func newTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add v1alpha1 scheme: %v", err)
	}
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add hv1 scheme: %v", err)
	}
	if err := vmv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add vmv1alpha1 scheme: %v", err)
	}
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&v1alpha1.Reservation{}).
		Build()
}

// failoverReservation builds a minimal failover host reservation targeting the
// given host, matching the shape the planner produces.
func failoverReservation(host string) v1alpha1.Reservation {
	return v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "failover-host-" + host,
			Labels: map[string]string{v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelFailoverHost},
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeFailoverHost,
			TargetHost: host,
		},
	}
}

func reconcileReq(name string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Name: name}}
}

// Reconcile covers the four reconcile branches: desired-but-absent (create),
// present-but-undesired (delete), present-and-drifted (update), and in-sync
// (no-op). Each branch is driven by seeding desiredReservations and the fake
// cluster independently, then asserting the resulting cluster state.

func TestReconcileCreatesDesiredReservation(t *testing.T) {
	c := &Controller{Client: newTestClient(t)}
	desired := failoverReservation("hv1")
	c.desiredReservations = map[corev1.ObjectReference]v1alpha1.Reservation{
		{Name: desired.Name}: desired,
	}
	req := reconcileReq(desired.Name)
	if _, err := c.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := &v1alpha1.Reservation{}
	if err := c.Get(context.Background(), req.NamespacedName, got); err != nil {
		t.Fatalf("expected reservation to be created: %v", err)
	}
	if got.Spec.TargetHost != "hv1" {
		t.Errorf("target host = %q, want hv1", got.Spec.TargetHost)
	}
}

func TestReconcileDeletesStaleReservation(t *testing.T) {
	stale := failoverReservation("hvX")
	c := &Controller{Client: newTestClient(t, &stale)}
	c.desiredReservations = map[corev1.ObjectReference]v1alpha1.Reservation{}
	req := reconcileReq(stale.Name)
	if _, err := c.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := &v1alpha1.Reservation{}
	if err := c.Get(context.Background(), req.NamespacedName, got); !apierrors.IsNotFound(err) {
		t.Errorf("expected reservation to be deleted, got err=%v", err)
	}
}

func TestReconcileUpdatesDriftedReservation(t *testing.T) {
	actual := failoverReservation("hv1")
	actual.Spec.AvailabilityZone = "az-old"
	c := &Controller{Client: newTestClient(t, &actual)}
	desired := failoverReservation("hv1")
	desired.Spec.AvailabilityZone = "az-new"
	c.desiredReservations = map[corev1.ObjectReference]v1alpha1.Reservation{
		{Name: desired.Name}: desired,
	}
	req := reconcileReq(desired.Name)
	if _, err := c.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := &v1alpha1.Reservation{}
	if err := c.Get(context.Background(), req.NamespacedName, got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Spec.AvailabilityZone != "az-new" {
		t.Errorf("availability zone = %q, want az-new", got.Spec.AvailabilityZone)
	}
}

func TestReconcileNoopWhenInSync(t *testing.T) {
	actual := failoverReservation("hv1")
	c := &Controller{Client: newTestClient(t, &actual)}
	desired := failoverReservation("hv1")
	c.desiredReservations = map[corev1.ObjectReference]v1alpha1.Reservation{
		{Name: desired.Name}: desired,
	}
	req := reconcileReq(desired.Name)
	before := &v1alpha1.Reservation{}
	if err := c.Get(context.Background(), req.NamespacedName, before); err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := c.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	after := &v1alpha1.Reservation{}
	if err := c.Get(context.Background(), req.NamespacedName, after); err != nil {
		t.Fatalf("get: %v", err)
	}
	// An update bumps the resourceVersion, so an unchanged one proves the no-op.
	if before.ResourceVersion != after.ResourceVersion {
		t.Errorf("reservation was rewritten: %q -> %q", before.ResourceVersion, after.ResourceVersion)
	}
}

// buildHV builds a hypervisor with an availability-zone label and the given
// memory (GiB) and cpu capacity, so collect accepts it and the planner can
// order hosts by cost.
func buildHV(name, az string, memGi, cpu int64) *hv1.Hypervisor {
	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{corev1.LabelTopologyZone: az},
		},
		Status: hv1.HypervisorStatus{
			Capacity: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: *resource.NewQuantity(memGi*1024*1024*1024, resource.BinarySI),
				hv1.ResourceCPU:    *resource.NewQuantity(cpu, resource.DecimalSI),
			},
		},
	}
}

// buildVM builds a virtual machine that Nova reports as placed on the given
// host, so collect indexes it under that host rather than skipping it.
func buildVM(name, host, az string) *vmv1alpha1.VirtualMachine {
	return &vmv1alpha1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: vmv1alpha1.VMSpec{
			AZ: az,
			Resources: map[vmv1alpha1.ResourceName]resource.Quantity{
				vmv1alpha1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
				vmv1alpha1.ResourceCPU:    *resource.NewQuantity(1, resource.DecimalSI),
			},
		},
		Status: vmv1alpha1.VMStatus{
			Nova: &vmv1alpha1.NovaStatus{Host: host},
		},
	}
}

// TestLoopGeneratesReservations exercises the whole pipeline end-to-end against
// a fake cluster and a stub external scheduler: collect -> FindPaths (via HTTP)
// -> BuildPartitions -> PlanReservations -> publish. It asserts the loop turns a
// pair of mutually-evacuable hosts into a desired failover host reservation.
func TestLoopGeneratesReservations(t *testing.T) {
	// Two equally-sized hosts each running one vm. The external scheduler says
	// each vm can evacuate onto the other host, so they form one partition.
	hvA := buildHV("hvA", "az1", 64, 32)
	hvB := buildHV("hvB", "az1", 64, 32)
	vmA := buildVM("vmA", "hvA", "az1")
	vmB := buildVM("vmB", "hvB", "az1")

	// Stub scheduler: answer each evacuation query with the other host, so the
	// path finder discovers the mutual edges hvA<->hvB.
	finder, server := stubScheduler(t, map[string][]string{
		"vmA": {"hvB"},
		"vmB": {"hvA"},
	}, nil)
	defer server.Close()

	// Wire the pipeline stages explicitly (loop is driven directly here, not via
	// SetupWithManager), using the stub path finder.
	c := &Controller{
		Client:             newTestClient(t, hvA, hvB, vmA, vmB),
		Config:             ControllerConfig{Ratio: 0.5, Minimum: 1},
		reconcileCh:        make(chan event.GenericEvent, 16),
		pathFinder:         finder,
		partitionBuilder:   cliquePartitionBuilder{},
		reservationPlanner: ratioReservationPlanner{ratio: 0.5, minimum: 1},
	}
	c.loop(context.Background())

	c.desiredReservationsMu.Lock()
	defer c.desiredReservationsMu.Unlock()
	if len(c.desiredReservations) == 0 {
		t.Fatalf("expected at least one desired reservation, got none")
	}
	for _, r := range c.desiredReservations {
		if r.Spec.Type != v1alpha1.ReservationTypeFailoverHost {
			t.Errorf("unexpected reservation type %q", r.Spec.Type)
		}
		if r.Spec.AvailabilityZone != "az1" {
			t.Errorf("availability zone = %q, want az1", r.Spec.AvailabilityZone)
		}
	}
}
