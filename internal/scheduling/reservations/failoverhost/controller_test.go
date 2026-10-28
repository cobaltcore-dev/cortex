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

// buildHV builds a hypervisor in az1 with an availability-zone label and the
// given memory (GiB) and cpu capacity, so collect accepts it and the planner can
// order hosts by cost.
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

// fakeSeeder returns a fixed set of seeds, so loop tests drive the pipeline
// without a Nova client.
type fakeSeeder struct{ seeds []Seed }

func (f fakeSeeder) Seeds(context.Context, topology) ([]Seed, error) { return f.seeds, nil }

// TestLoopGeneratesReservations exercises the whole pipeline end-to-end against
// a fake cluster and a stub external scheduler: collect -> Seeds -> FindPaths
// (via HTTP) -> BuildPartitions -> PlanReservations -> publish. It asserts the
// loop turns a pair of mutually-evacuable hosts into a desired failover host
// reservation.
func TestLoopGeneratesReservations(t *testing.T) {
	// Two equally-sized hosts. A single flavor seed is accepted on both, so the
	// path finder discovers the mutual edges hvA<->hvB and they form one partition.
	hvA := buildHV("hvA", 64, 32)
	hvB := buildHV("hvB", 64, 32)

	finder, server := stubScheduler(t, map[string][]string{
		"flavor": {"hvA", "hvB"},
	}, nil)
	defer server.Close()

	// Wire the pipeline stages explicitly (loop is driven directly here, not via
	// SetupWithManager), using the fake seeder and stub path finder.
	c := &Controller{
		Client:             newTestClient(t, hvA, hvB),
		Config:             ControllerConfig{Ratio: 0.5, Minimum: 1},
		reconcileCh:        make(chan event.GenericEvent, 16),
		seeder:             fakeSeeder{seeds: []Seed{seed("flavor")}},
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

// fakePartitionBuilder returns a fixed set of partitions regardless of input, so
// a test can drive loop with overlapping cliques the real builder would produce.
type fakePartitionBuilder struct{ partitions []Partition }

func (f fakePartitionBuilder) BuildPartitions([]Path) []Partition {
	return f.partitions
}

// TestLoopDeduplicatesSharedHost checks that when overlapping maximal cliques
// share a host (hvB in both {hvA,hvB} and {hvB,hvC}), loop assigns that host to
// exactly one partition before planning, so planning never sees hvB in two
// partitions and cannot emit two reservations with the same name. The two
// partitions are equal size, so the deterministic tiebreak keeps hvB in the one
// with the smaller first host name ({hvA,hvB}); the other shrinks to the single
// host hvC, too small to reserve. The surviving {hvA,hvB} partition reserves one
// target (hvA, the first equal-cost host), whose only source host is hvB — proof
// hvB was planned once, as a source, not as a conflicting second target.
func TestLoopDeduplicatesSharedHost(t *testing.T) {
	hvA := buildHV("hvA", 64, 32)
	hvB := buildHV("hvB", 64, 32)
	hvC := buildHV("hvC", 64, 32)

	// The path finder's edges are irrelevant here: the fake builder ignores them
	// and hands loop the overlapping cliques directly. The stub still needs an
	// answer so FindPaths succeeds; one seed accepted on all three suffices.
	finder, server := stubScheduler(t, map[string][]string{
		"flavor": {"hvA", "hvB", "hvC"},
	}, nil)
	defer server.Close()

	c := &Controller{
		Client:      newTestClient(t, hvA, hvB, hvC),
		Config:      ControllerConfig{Ratio: 0.5, Minimum: 1},
		reconcileCh: make(chan event.GenericEvent, 16),
		seeder:      fakeSeeder{seeds: []Seed{seed("flavor")}},
		pathFinder:  finder,
		partitionBuilder: fakePartitionBuilder{partitions: []Partition{
			{Hosts: []hv1.Hypervisor{*hvA, *hvB}}, {Hosts: []hv1.Hypervisor{*hvB, *hvC}},
		}},
		reservationPlanner: ratioReservationPlanner{ratio: 0.5, minimum: 1},
	}
	c.loop(context.Background())

	c.desiredReservationsMu.Lock()
	defer c.desiredReservationsMu.Unlock()
	// Exactly one reservation survives: {hvA,hvB} reserves hvA, whose source host
	// is the deduped hvB. {hvB,hvC} lost hvB and shrank to lone hvC, reserving
	// nothing.
	if len(c.desiredReservations) != 1 {
		t.Fatalf("desired reservations = %d, want 1", len(c.desiredReservations))
	}
	aRes, ok := c.desiredReservations[corev1.ObjectReference{Name: "failover-host-hvA"}]
	if !ok {
		t.Fatalf("expected a reservation targeting hvA")
	}
	if got := aRes.Spec.FailoverHostReservation.SourceHosts; len(got) != 1 || got[0] != "hvB" {
		t.Errorf("hvA source hosts = %v, want [hvB]", got)
	}
}

// TestLoopSharedHostKeptInSmallestPartition checks that when a shared host sits
// in partitions of different sizes, loop assigns it to the smallest one, keeping
// the most partitions alive. Here hvC is shared between {hvA,hvB,hvC} (size 3)
// and {hvC,hvD} (size 2). Keeping hvC in the smaller {hvC,hvD} lets that
// partition survive, while {hvA,hvB,hvC} merely shrinks to {hvA,hvB} and stays
// above the two-host minimum — so both partitions retain a failover target.
// Keeping hvC in the larger partition instead would collapse {hvC,hvD} to a lone
// host and leave hvD unprotected.
func TestLoopSharedHostKeptInSmallestPartition(t *testing.T) {
	hvA := buildHV("hvA", 64, 32)
	hvB := buildHV("hvB", 64, 32)
	hvC := buildHV("hvC", 64, 32)
	hvD := buildHV("hvD", 64, 32)

	finder, server := stubScheduler(t, map[string][]string{
		"flavor": {"hvA", "hvB", "hvC", "hvD"},
	}, nil)
	defer server.Close()

	c := &Controller{
		Client:      newTestClient(t, hvA, hvB, hvC, hvD),
		Config:      ControllerConfig{Ratio: 0.5, Minimum: 1},
		reconcileCh: make(chan event.GenericEvent, 16),
		seeder:      fakeSeeder{seeds: []Seed{seed("flavor")}},
		pathFinder:  finder,
		partitionBuilder: fakePartitionBuilder{partitions: []Partition{
			{Hosts: []hv1.Hypervisor{*hvA, *hvB, *hvC}}, {Hosts: []hv1.Hypervisor{*hvC, *hvD}},
		}},
		reservationPlanner: ratioReservationPlanner{ratio: 0.5, minimum: 1},
	}
	c.loop(context.Background())

	c.desiredReservationsMu.Lock()
	defer c.desiredReservationsMu.Unlock()
	// Both partitions survive: {hvC,hvD} keeps the shared hvC and {hvA,hvB,hvC}
	// shrinks to {hvA,hvB}. Each reserves one target, so the shared host did not
	// cost either partition its coverage.
	if len(c.desiredReservations) != 2 {
		t.Fatalf("desired reservations = %d, want 2", len(c.desiredReservations))
	}
	// {hvC,hvD} must still exist, proving hvC stayed in the smaller partition.
	// Whichever of hvC/hvD is reserved, its sole source host is the other.
	cdReserved := false
	for name, r := range c.desiredReservations {
		sources := r.Spec.FailoverHostReservation.SourceHosts
		if name.Name == "failover-host-hvC" && len(sources) == 1 && sources[0] == "hvD" {
			cdReserved = true
		}
		if name.Name == "failover-host-hvD" && len(sources) == 1 && sources[0] == "hvC" {
			cdReserved = true
		}
	}
	if !cdReserved {
		t.Errorf("expected a {hvC,hvD} reservation, got %v", c.desiredReservations)
	}
}
