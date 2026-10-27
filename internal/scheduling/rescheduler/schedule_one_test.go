// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package rescheduler

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	vmv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type placementBackendFunc func(context.Context, PlacementRequest) PlacementResult

func (f placementBackendFunc) Schedule(ctx context.Context, req PlacementRequest) PlacementResult {
	return f(ctx, req)
}

func TestOrderEntities(t *testing.T) {
	entityWith := func(ref string, n int) entityCandidates {
		candidates := make([]string, n)
		for i := range candidates {
			candidates[i] = "host"
		}
		return entityCandidates{
			entity:     entity{kind: v1alpha1.DrainEntityKindVM, ref: ref},
			candidates: candidates,
		}
	}
	refsOf := func(pes []entityCandidates) []string {
		refs := make([]string, len(pes))
		for i, pe := range pes {
			refs[i] = pe.ref
		}
		return refs
	}

	t.Run("fewest options first", func(t *testing.T) {
		pes := []entityCandidates{entityWith("a", 2), entityWith("b", 1), entityWith("c", 3)}
		orderEntities(pes, 5)
		got := refsOf(pes)
		want := []string{"b", "a", "c"}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	})

	t.Run("candidate count saturates", func(t *testing.T) {
		// With saturation 2, entities with 2 and 5 options are equal, so stable
		// order is preserved and the one with 1 option still comes first.
		pes := []entityCandidates{entityWith("plenty", 5), entityWith("scarce", 1), entityWith("enough", 2)}
		orderEntities(pes, 2)
		got := refsOf(pes)
		want := []string{"scarce", "plenty", "enough"}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	})

	t.Run("bigger first among equal options", func(t *testing.T) {
		small := entityWith("small", 3)
		small.size = 1
		big := entityWith("big", 3)
		big.size = 100
		pes := []entityCandidates{small, big}
		orderEntities(pes, 2)
		if got := refsOf(pes); got[0] != "big" || got[1] != "small" {
			t.Fatalf("expected big before small, got %v", got)
		}
	})
}

func TestEntitySize(t *testing.T) {
	vm := &vmv1alpha1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{Name: "vm-1"},
		Spec: vmv1alpha1.VMSpec{
			Resources: map[vmv1alpha1.ResourceName]resource.Quantity{
				vmv1alpha1.ResourceCPU:    resource.MustParse("4"),
				vmv1alpha1.ResourceMemory: resource.MustParse("8Gi"),
			},
		},
	}
	s := newScheduler(t, vm)
	ctx := context.Background()

	if got := s.entitySize(ctx, entity{kind: v1alpha1.DrainEntityKindVM, ref: "vm-1"}); got != 8*1024*1024*1024 {
		t.Errorf("expected 8Gi, got %d", got)
	}
	// Fallback: unknown vm resolves to zero.
	if got := s.entitySize(ctx, entity{kind: v1alpha1.DrainEntityKindVM, ref: "missing"}); got != 0 {
		t.Errorf("expected 0 for missing vm, got %d", got)
	}
	// Non-vm entities have no vm-operator size.
	if got := s.entitySize(ctx, entity{kind: v1alpha1.DrainEntityKindReservation, ref: "vm-1"}); got != 0 {
		t.Errorf("expected 0 for non-vm entity, got %d", got)
	}
}

func TestScheduleOne_SchedulesAcknowledged(t *testing.T) {
	rr := request("req-ack", v1alpha1.ReschedulingRequestStateAcknowledged)
	s := newScheduler(t, rr)
	defer startBroker(s)()

	s.scheduleOne(context.Background(), getRequest(t, s, "req-ack"))

	if got := stateOf(t, s, "req-ack"); got != v1alpha1.ReschedulingRequestStateAccepted {
		t.Errorf("expected Accepted after scheduling, got %s", got)
	}
}

func TestScheduleOne_RejectsWhenNoHost(t *testing.T) {
	rr := request("req-ack", v1alpha1.ReschedulingRequestStateAcknowledged)
	s := newScheduler(t, rr)
	s.Placement = fakeBackend{result: PlacementResult{Candidates: nil}}
	defer startBroker(s)()

	s.scheduleOne(context.Background(), getRequest(t, s, "req-ack"))

	if got := stateOf(t, s, "req-ack"); got != v1alpha1.ReschedulingRequestStateRejected {
		t.Errorf("expected Rejected when no host found, got %s", got)
	}
}

func TestScheduleOne_FailsOnPlacementError(t *testing.T) {
	rr := request("req-ack", v1alpha1.ReschedulingRequestStateAcknowledged)
	s := newScheduler(t, rr)
	s.Placement = fakeBackend{result: PlacementResult{Err: errors.New("boom")}}
	defer startBroker(s)()

	s.scheduleOne(context.Background(), getRequest(t, s, "req-ack"))

	if got := stateOf(t, s, "req-ack"); got != v1alpha1.ReschedulingRequestStateFailed {
		t.Errorf("expected Failed on placement error, got %s", got)
	}
}

func TestScheduleOne_RecordsMovesPerEntity(t *testing.T) {
	rr := drainRequest("multi", false,
		drainHost{"host-a", []string{"vm-1"}},
		drainHost{"host-b", []string{"vm-2"}},
	)
	rr.Status.State = v1alpha1.ReschedulingRequestStateAcknowledged
	s := newScheduler(t, rr)
	s.Placement = perEntityBackend{byUUID: map[string][]string{
		"vm-1": {"target-1"},
		"vm-2": {"target-2a", "target-2b"},
	}}
	defer startBroker(s)()

	s.scheduleOne(context.Background(), getRequest(t, s, "multi"))

	got := getRequest(t, s, "multi")
	if got.Status.State != v1alpha1.ReschedulingRequestStateAccepted {
		t.Fatalf("expected Accepted, got %s", got.Status.State)
	}
	moves := map[string]v1alpha1.ReschedulingMove{}
	for _, m := range got.Status.Moves {
		moves[m.EntityRef] = m
	}
	if len(moves) != 2 {
		t.Fatalf("expected 2 moves, got %d (%+v)", len(moves), got.Status.Moves)
	}
	if m := moves["vm-1"]; m.SourceHost != "host-a" || m.TargetHost != "target-1" {
		t.Errorf("vm-1 move wrong: %+v", m)
	}
	if m := moves["vm-2"]; m.SourceHost != "host-b" || m.TargetHost != "target-2a" {
		t.Errorf("vm-2 move should take best candidate: %+v", m)
	}
}

func TestScheduleOne_DiscoversThenReservesFromShortlist(t *testing.T) {
	rr := request("two-pass", v1alpha1.ReschedulingRequestStateAcknowledged)
	s := newScheduler(t, rr)
	var calls []PlacementRequest
	s.Placement = placementBackendFunc(func(_ context.Context, req PlacementRequest) PlacementResult {
		calls = append(calls, req)
		if req.Options.ReadOnly {
			return PlacementResult{Candidates: []string{"host-a", "host-b", "host-c"}}
		}
		return PlacementResult{Candidates: []string{"host-b"}}
	})
	defer startBroker(s)()

	s.scheduleOne(context.Background(), getRequest(t, s, "two-pass"))

	if len(calls) != 2 {
		t.Fatalf("expected discovery and reservation calls, got %d", len(calls))
	}
	discovery := calls[0]
	if !discovery.Options.ReadOnly || !discovery.Options.SkipInflight ||
		!discovery.Options.SkipCommittedResourceTracking {
		t.Errorf("unexpected discovery options: %+v", discovery.Options)
	}
	reservation := calls[1]
	if reservation.Options.ReadOnly || !reservation.Options.LockReservations ||
		reservation.Options.MaxCandidates != 1 || reservation.Options.SkipInflight ||
		!reservation.Options.SkipCommittedResourceTracking {
		t.Errorf("unexpected reservation options: %+v", reservation.Options)
	}
	if !slices.Equal(reservation.AllowedHosts, []string{"host-a", "host-b", "host-c"}) {
		t.Errorf("allowed hosts = %v, want discovered candidates", reservation.AllowedHosts)
	}
	got := getRequest(t, s, "two-pass")
	if len(got.Status.Moves) != 1 || got.Status.Moves[0].TargetHost != "host-b" {
		t.Errorf("expected reservation-time target host-b, got %+v", got.Status.Moves)
	}
}

func TestScheduleOne_RejectsCandidateGoneBeforeReservation(t *testing.T) {
	rr := request("gone", v1alpha1.ReschedulingRequestStateAcknowledged)
	s := newScheduler(t, rr)
	call := 0
	s.Placement = placementBackendFunc(func(_ context.Context, _ PlacementRequest) PlacementResult {
		call++
		if call == 1 {
			return PlacementResult{Candidates: []string{"host-a"}}
		}
		return PlacementResult{Candidates: nil}
	})
	defer startBroker(s)()

	s.scheduleOne(context.Background(), getRequest(t, s, "gone"))

	if call != 2 {
		t.Fatalf("expected candidate to be revalidated, got %d calls", call)
	}
	if got := stateOf(t, s, "gone"); got != v1alpha1.ReschedulingRequestStateRejected {
		t.Errorf("expected Rejected when candidate disappeared, got %s", got)
	}
}

func TestScheduleOne_ReserveTakesHostBlock(t *testing.T) {
	rr := drainRequest("drain", true, drainHost{"src-host", []string{"vm-1"}})
	rr.Status.State = v1alpha1.ReschedulingRequestStateAcknowledged
	s := newScheduler(t, rr)
	s.Placement = StubPlacementBackend{Host: "target"}
	defer startBroker(s)()

	s.scheduleOne(context.Background(), getRequest(t, s, "drain"))

	got := getRequest(t, s, "drain")
	if got.Status.State != v1alpha1.ReschedulingRequestStateAccepted {
		t.Fatalf("expected Accepted, got %s", got.Status.State)
	}
	if len(got.Status.BlockedHosts) != 1 || got.Status.BlockedHosts[0] != "src-host" {
		t.Errorf("expected src-host blocked while active, got %v", got.Status.BlockedHosts)
	}
}

func TestScheduleOne_RejectsWhenEntityUnplaceable(t *testing.T) {
	rr := drainRequest("drain", true, drainHost{"src-host", []string{"vm-1"}})
	rr.Status.State = v1alpha1.ReschedulingRequestStateAcknowledged
	s := newScheduler(t, rr)
	s.Placement = fakeBackend{result: PlacementResult{Candidates: nil}}
	defer startBroker(s)()

	s.scheduleOne(context.Background(), getRequest(t, s, "drain"))

	got := getRequest(t, s, "drain")
	if got.Status.State != v1alpha1.ReschedulingRequestStateRejected {
		t.Fatalf("expected Rejected, got %s", got.Status.State)
	}
	if len(got.Status.BlockedHosts) != 0 {
		t.Errorf("expected host-block released on rollback, got %v", got.Status.BlockedHosts)
	}
}

func TestScheduleOne_BestEffortPlacesSubset(t *testing.T) {
	rr := drainRequest("be", false,
		drainHost{"host-a", []string{"vm-1", "vm-2"}},
	)
	rr.Spec.Drain.Constraints.Mode = v1alpha1.DrainConstraintModeBestEffort
	rr.Status.State = v1alpha1.ReschedulingRequestStateAcknowledged
	s := newScheduler(t, rr)
	s.Placement = perEntityBackend{byUUID: map[string][]string{
		"vm-1": {"target-1"},
		// vm-2 has no candidates.
	}}
	defer startBroker(s)()

	s.scheduleOne(context.Background(), getRequest(t, s, "be"))

	got := getRequest(t, s, "be")
	if got.Status.State != v1alpha1.ReschedulingRequestStateAccepted {
		t.Fatalf("expected Accepted, got %s", got.Status.State)
	}
	if len(got.Status.Moves) != 1 || got.Status.Moves[0].EntityRef != "vm-1" {
		t.Errorf("expected only vm-1 moved, got %+v", got.Status.Moves)
	}
}

func TestScheduleOne_RejectsWhenNothingPlaceable(t *testing.T) {
	rr := drainRequest("be", false, drainHost{"host-a", []string{"vm-1", "vm-2"}})
	rr.Spec.Drain.Constraints.Mode = v1alpha1.DrainConstraintModeBestEffort
	rr.Status.State = v1alpha1.ReschedulingRequestStateAcknowledged
	s := newScheduler(t, rr)
	s.Placement = fakeBackend{result: PlacementResult{Candidates: nil}}
	defer startBroker(s)()

	s.scheduleOne(context.Background(), getRequest(t, s, "be"))

	if got := stateOf(t, s, "be"); got != v1alpha1.ReschedulingRequestStateRejected {
		t.Errorf("expected Rejected when nothing placeable, got %s", got)
	}
}

func TestScheduleOne_DirtyStartFails(t *testing.T) {
	// An Acknowledged request carrying a leftover host-block is dirty.
	rr := request("dirty", v1alpha1.ReschedulingRequestStateAcknowledged)
	rr.Status.BlockedHosts = []string{"leftover"}
	s := newScheduler(t, rr)
	defer startBroker(s)()

	s.scheduleOne(context.Background(), getRequest(t, s, "dirty"))

	got := getRequest(t, s, "dirty")
	if got.Status.State != v1alpha1.ReschedulingRequestStateFailed {
		t.Fatalf("expected Failed on dirty start, got %s", got.Status.State)
	}
	if len(got.Status.BlockedHosts) != 0 {
		t.Errorf("expected host-block cleared on failure, got %v", got.Status.BlockedHosts)
	}
}
