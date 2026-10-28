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
// given host, matching the shape the planner produces. Its resources match the
// capacity buildHV(host, 64, 32) gives the hypervisor, so the controller's
// hypervisor-size check passes.
func failoverReservation(host string) v1alpha1.Reservation {
	return v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "failover-host-" + host,
			Labels: map[string]string{v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelFailoverHost},
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeFailoverHost,
			TargetHost: host,
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: *resource.NewQuantity(64*1024*1024*1024, resource.BinarySI),
				hv1.ResourceCPU:    *resource.NewQuantity(32, resource.DecimalSI),
			},
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
	c := &Controller{Client: newTestClient(t, buildHV("hv1", 64, 32))}
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
	c := &Controller{Client: newTestClient(t, &actual, buildHV("hv1", 64, 32))}
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
	c := &Controller{Client: newTestClient(t, &actual, buildHV("hv1", 64, 32))}
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

func TestReconcileErrorsWhenHypervisorMissing(t *testing.T) {
	c := &Controller{Client: newTestClient(t)}
	desired := failoverReservation("hv1")
	c.desiredReservations = map[corev1.ObjectReference]v1alpha1.Reservation{
		{Name: desired.Name}: desired,
	}
	if _, err := c.Reconcile(context.Background(), reconcileReq(desired.Name)); err == nil {
		t.Fatal("expected an error to trigger backoff when the hypervisor is missing")
	}
}

func TestReconcileErrorsWhenHypervisorSizeMismatches(t *testing.T) {
	// The hypervisor reports 32Gi but the reservation reserves 64Gi.
	c := &Controller{Client: newTestClient(t, buildHV("hv1", 32, 32))}
	desired := failoverReservation("hv1")
	c.desiredReservations = map[corev1.ObjectReference]v1alpha1.Reservation{
		{Name: desired.Name}: desired,
	}
	if _, err := c.Reconcile(context.Background(), reconcileReq(desired.Name)); err == nil {
		t.Fatal("expected an error to trigger backoff on a memory mismatch")
	}
}

func TestReconcileErrorsWhenHypervisorCPUMismatches(t *testing.T) {
	// Memory matches but the hypervisor reports 16 CPUs against the reserved 32.
	c := &Controller{Client: newTestClient(t, buildHV("hv1", 64, 16))}
	desired := failoverReservation("hv1")
	c.desiredReservations = map[corev1.ObjectReference]v1alpha1.Reservation{
		{Name: desired.Name}: desired,
	}
	if _, err := c.Reconcile(context.Background(), reconcileReq(desired.Name)); err == nil {
		t.Fatal("expected an error to trigger backoff on a cpu mismatch")
	}
}

// buildHV builds a hypervisor in az1 with an availability-zone label and the
// given memory (GiB) and cpu capacity, so collect accepts it and the planner can
// order hosts by cost. It is shared by the generator tests (generation_test.go)
// and the path finder tests (pathfinding_test.go).
func buildHV(name string, memGi, cpu int64) *hv1.Hypervisor {
	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{corev1.LabelTopologyZone: "az1"},
		},
		Status: hv1.HypervisorStatus{
			Capacity: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: *resource.NewQuantity(memGi*1024*1024*1024, resource.BinarySI),
				hv1.ResourceCPU:    *resource.NewQuantity(cpu, resource.DecimalSI),
			},
		},
	}
}

// fakeGenerator is a stub ReservationGenerator returning fixed output, so the
// controller's loop plumbing can be tested without the real pipeline.
type fakeGenerator struct {
	desired map[corev1.ObjectReference]v1alpha1.Reservation
	topo    topology
	ok      bool
}

func (f fakeGenerator) Generate(context.Context) (map[corev1.ObjectReference]v1alpha1.Reservation, topology, bool) {
	return f.desired, f.topo, f.ok
}

// TestLoopPublishesGeneratorOutput checks the controller half: loop swaps the
// generator's desired reservations in as the new desired state.
func TestLoopPublishesGeneratorOutput(t *testing.T) {
	desired := failoverReservation("hvA")
	c := &Controller{
		Client:      newTestClient(t),
		reconcileCh: make(chan event.GenericEvent, 16),
		generator: fakeGenerator{
			desired: map[corev1.ObjectReference]v1alpha1.Reservation{{Name: desired.Name}: desired},
			ok:      true,
		},
	}
	c.loop(context.Background())

	c.desiredReservationsMu.Lock()
	defer c.desiredReservationsMu.Unlock()
	if _, ok := c.desiredReservations[corev1.ObjectReference{Name: desired.Name}]; !ok {
		t.Fatalf("expected generator output to become the desired state")
	}
}

// TestLoopKeepsDesiredStateWhenGeneratorAborts checks that an aborting generator
// (ok=false) leaves the previously published desired state untouched, so a
// partial recalculation never drops still-valid reservations.
func TestLoopKeepsDesiredStateWhenGeneratorAborts(t *testing.T) {
	kept := failoverReservation("hvKept")
	c := &Controller{
		Client:              newTestClient(t),
		reconcileCh:         make(chan event.GenericEvent, 16),
		desiredReservations: map[corev1.ObjectReference]v1alpha1.Reservation{{Name: kept.Name}: kept},
		generator:           fakeGenerator{ok: false},
	}
	c.loop(context.Background())

	c.desiredReservationsMu.Lock()
	defer c.desiredReservationsMu.Unlock()
	if len(c.desiredReservations) != 1 {
		t.Fatalf("desired reservations = %d, want the previous 1 kept", len(c.desiredReservations))
	}
	if _, ok := c.desiredReservations[corev1.ObjectReference{Name: kept.Name}]; !ok {
		t.Errorf("expected the previously published reservation to survive an aborted pass")
	}
}
