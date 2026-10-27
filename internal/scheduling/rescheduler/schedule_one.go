// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package rescheduler

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/cobaltcore-dev/cortex/api/scheduling"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	vmv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// scheduleOne performs the full scheduling pass for a single, already-dequeued
// Acknowledged request. It claims the request (Acknowledged -> Processing),
// verifies a clean start, takes the host-block (if requested), dry-run places
// and orders the entities, reserves the required moves, and commits the request
// to Accepted recording status.moves, or rolls back and declines (Rejected when
// merely infeasible, Failed otherwise).
func (s *Scheduler) scheduleOne(ctx context.Context, rr *v1alpha1.ReschedulingRequest) {
	log := logf.FromContext(ctx)
	log.Info("scheduling rescheduling request", "request", rr.Name)

	// Claim the request.
	if err := s.updateStatus(ctx, rr, func() {
		rr.Status.State = v1alpha1.ReschedulingRequestStateProcessing
	}); err != nil {
		log.Error(err, "failed to mark request processing", "request", rr.Name)
		return
	}

	// A request entering Processing must start clean; a dirty/stale start state
	// (e.g. a leftover host-block or, later, leftover inflight holds) fails it.
	if err := checkCleanStart(rr); err != nil {
		log.Error(err, "dirty start state, failing request", "request", rr.Name)
		s.fail(ctx, rr)
		return
	}

	// Block the drained host(s) from incoming placement while the request is
	// active (only for a drain with reserve).
	if err := s.takeHostBlock(ctx, rr); err != nil {
		log.Error(err, "failed to take host-block, failing request", "request", rr.Name)
		s.fail(ctx, rr)
		return
	}

	// Discover candidate targets for every entity without taking holds, excluding
	// all drained hosts (an entity is never relocated onto another drained host).
	candidates, err := s.placementCandidates(ctx, rr)
	if err != nil {
		// A placement backend error is an infrastructure failure, not a clean
		// rejection: roll back and fail.
		log.Error(err, "placement failed, rolling back", "request", rr.Name)
		s.declineOrFail(ctx, rr, false)
		return
	}

	// Order entities and reserve the required moves (holds are a no-op today).
	moves, reserved, err := s.reserveMoves(ctx, rr, candidates)
	if err != nil {
		log.Error(err, "reservation scheduling failed, rolling back", "request", rr.Name)
		s.declineOrFail(ctx, rr, false)
		return
	}
	if !reserved {
		log.Info("cannot reserve all required moves, rolling back", "request", rr.Name)
		s.declineOrFail(ctx, rr, true)
		return
	}

	if err := s.updateStatus(ctx, rr, func() {
		rr.Status.Moves = moves
		rr.Status.State = v1alpha1.ReschedulingRequestStateAccepted
	}); err != nil {
		log.Error(err, "failed to accept request, rolling back", "request", rr.Name)
		s.declineOrFail(ctx, rr, false)
	}
}

// checkCleanStart verifies a request entering Processing carries no leftover
// artifacts from an interrupted earlier attempt. A fresh request must not
// already hold a host-block. Leftover inflight holds will be detected here too
// once inflight integration lands; for now that part is a no-op.
func checkCleanStart(rr *v1alpha1.ReschedulingRequest) error {
	if len(rr.Status.BlockedHosts) > 0 {
		return fmt.Errorf("leftover host-block from an interrupted attempt: %v", rr.Status.BlockedHosts)
	}
	return nil
}

// takeHostBlock records the drained hosts on the request so the drain-block
// filter excludes them from incoming placement. It is a no-op unless the request
// is a drain with reserve set.
func (s *Scheduler) takeHostBlock(ctx context.Context, rr *v1alpha1.ReschedulingRequest) error {
	if rr.Spec.Drain == nil || !rr.Spec.Drain.Reserve {
		return nil
	}
	hosts := drainHosts(rr)
	if len(hosts) == 0 {
		return nil
	}
	return s.updateStatus(ctx, rr, func() {
		rr.Status.BlockedHosts = hosts
	})
}

// placementCandidates discovers candidate targets for every entity without
// taking holds. All drained hosts are excluded as destinations; the candidates
// are then filtered by rescheduler policy and truncated to the configured
// reservation shortlist before entities are ordered.
func (s *Scheduler) placementCandidates(ctx context.Context, rr *v1alpha1.ReschedulingRequest) ([]entityCandidates, error) {
	ignore := drainHosts(rr)
	entities := drainEntities(rr)
	candidates := make([]entityCandidates, 0, len(entities))

	for _, e := range entities {
		req := placementRequestFor(rr, e, ignore)
		req.Options = scheduling.Options{
			ReadOnly:                      true,
			SkipInflight:                  true,
			SkipCommittedResourceTracking: true,
		}
		result := s.place(ctx, req)
		if result.Err != nil {
			return nil, result.Err
		}
		filtered := s.filterCandidates(rr, e, result.Candidates)
		candidates = append(candidates, entityCandidates{
			entity:     e,
			candidates: filtered,
			size:       s.entitySize(ctx, e),
		})
	}
	return candidates, nil
}

// entitySize returns a comparable size for an entity, used as the big→small
// tiebreaker when ordering entities for reservation. It uses the memory the VM
// requests, read from its vm-operator VirtualMachine resource (keyed by the Nova
// instance uuid). If the entity is not a VM or its VirtualMachine cannot be
// looked up, it falls back to zero, which sorts the entity last among equals.
func (s *Scheduler) entitySize(ctx context.Context, e entity) uint64 {
	if e.kind != v1alpha1.DrainEntityKindVM {
		return 0
	}
	vm := &vmv1alpha1.VirtualMachine{}
	getCtx, cancel := s.apiCtx(ctx)
	defer cancel()
	if err := s.Get(getCtx, client.ObjectKey{Name: e.ref}, vm); err != nil {
		logf.FromContext(ctx).V(1).Info("could not look up vm size, falling back to zero", "vm", e.ref, "error", err)
		return 0
	}
	memory := vm.Spec.Resources[vmv1alpha1.ResourceMemory]
	if memory.Value() < 0 {
		return 0
	}
	return uint64(memory.Value()) //nolint:gosec // guarded against negative above
}

// filterCandidates is the per-entity rescheduler filter hook applied to the
// dry-run candidate targets. It is a pass-through no-op for now; the real policy
// filtering (do-not-move labels, host gating, etc.) is delivered later.
func (s *Scheduler) filterCandidates(_ *v1alpha1.ReschedulingRequest, _ entity, candidates []string) []string {
	return candidates
}

// reserveMoves orders the entities (fewest-options-first, big->small) and
// runs a fresh, constrained placement call for each move. The call revalidates
// the filtered shortlist and is the point where a hold will be acquired once
// inflight integration lands; today that hold creation is still a no-op.
// It reports whether the request can be satisfied: an all-mode request needs
// every entity placed, a best-effort request skips unplaceable entities, and in
// both modes a request with nothing placeable cannot be satisfied.
func (s *Scheduler) reserveMoves(
	ctx context.Context,
	rr *v1alpha1.ReschedulingRequest,
	candidates []entityCandidates,
) ([]v1alpha1.ReschedulingMove, bool, error) {

	orderEntities(candidates, s.CandidateSaturation)
	bestEffort := drainMode(rr) == v1alpha1.DrainConstraintModeBestEffort
	moves := make([]v1alpha1.ReschedulingMove, 0, len(candidates))
	ignore := drainHosts(rr)
	for _, ec := range candidates {
		if len(ec.candidates) == 0 {
			if !bestEffort {
				return nil, false, nil // an all-mode request needs every entity placed
			}
			continue // best-effort: skip an entity that cannot be placed
		}
		req := placementRequestFor(rr, ec.entity, ignore)
		req.AllowedHosts = ec.candidates
		req.Options = scheduling.Options{
			LockReservations:              true,
			MaxCandidates:                 1,
			SkipCommittedResourceTracking: true,
		}
		result := s.place(ctx, req)
		if result.Err != nil {
			return nil, false, result.Err
		}
		target := ""
		for _, candidate := range result.Candidates {
			if slices.Contains(ec.candidates, candidate) {
				target = candidate
				break
			}
		}
		if target == "" {
			if !bestEffort {
				return nil, false, nil
			}
			continue
		}
		moves = append(moves, v1alpha1.ReschedulingMove{
			EntityKind: ec.kind,
			EntityRef:  ec.ref,
			SourceHost: ec.sourceHost,
			TargetHost: target,
		})
	}
	if len(moves) == 0 {
		return nil, false, nil // nothing placeable -> reject
	}
	return moves, true, nil
}

// declineOrFail rolls back the request and records the outcome. Rollback
// releases inflight holds (a no-op today) and un-reserves the host (clears the
// host-block). A clean rollback becomes Rejected when rejectOnCleanRollback is
// set (the request was merely infeasible); otherwise, or if rollback fails, it
// becomes Failed.
func (s *Scheduler) declineOrFail(ctx context.Context, rr *v1alpha1.ReschedulingRequest, rejectOnCleanRollback bool) {
	log := logf.FromContext(ctx)
	target := v1alpha1.ReschedulingRequestStateFailed
	if err := s.releaseHolds(ctx, rr); err != nil {
		log.Error(err, "rollback failed, failing request", "request", rr.Name)
	} else if rejectOnCleanRollback {
		target = v1alpha1.ReschedulingRequestStateRejected
	}
	if err := s.updateStatus(ctx, rr, func() {
		rr.Status.BlockedHosts = nil // un-reserve the host
		rr.Status.State = target
	}); err != nil {
		log.Error(err, "failed to record request outcome", "request", rr.Name, "target", target)
	}
}

// releaseHolds releases any inflight holds acquired for the request. Inflight
// reservations are not created yet (their integration is a separate epic), so
// this is a best-effort no-op hook today.
func (s *Scheduler) releaseHolds(_ context.Context, _ *v1alpha1.ReschedulingRequest) error {
	return nil
}

// fail transitions the request to Failed, clearing any host-block it holds.
func (s *Scheduler) fail(ctx context.Context, rr *v1alpha1.ReschedulingRequest) {
	if err := s.updateStatus(ctx, rr, func() {
		rr.Status.BlockedHosts = nil
		rr.Status.State = v1alpha1.ReschedulingRequestStateFailed
	}); err != nil {
		logf.FromContext(ctx).Error(err, "failed to mark request failed", "request", rr.Name)
	}
}

// drainHosts returns the names of the hosts targeted by the request's drain.
func drainHosts(rr *v1alpha1.ReschedulingRequest) []string {
	if rr.Spec.Drain == nil {
		return nil
	}
	hosts := make([]string, 0, len(rr.Spec.Drain.Target.Hosts))
	for _, h := range rr.Spec.Drain.Target.Hosts {
		hosts = append(hosts, h.Host)
	}
	return hosts
}

// drainEntities flattens the per-host entities of the request's drain into a
// single list, remembering each entity's source host.
func drainEntities(rr *v1alpha1.ReschedulingRequest) []entity {
	if rr.Spec.Drain == nil {
		return nil
	}
	var entities []entity
	for _, h := range rr.Spec.Drain.Target.Hosts {
		for _, e := range h.Entities {
			entities = append(entities, entity{kind: e.Kind, ref: e.Ref, sourceHost: h.Host})
		}
	}
	return entities
}

// drainMode returns the constraint mode of the request's drain, defaulting to
// the all-or-nothing mode.
func drainMode(rr *v1alpha1.ReschedulingRequest) v1alpha1.DrainConstraintMode {
	if rr.Spec.Drain == nil || rr.Spec.Drain.Constraints.Mode == "" {
		return v1alpha1.DrainConstraintModeAll
	}
	return rr.Spec.Drain.Constraints.Mode
}

// placementRequestFor builds the initial-placement request for a single entity.
// It is provisional: it carries what the walking skeleton knows (the entity
// reference, the availability zone and the hosts to exclude as destinations);
// the real resource shape is filled in once data gathering lands.
func placementRequestFor(rr *v1alpha1.ReschedulingRequest, e entity, ignore []string) PlacementRequest {
	req := PlacementRequest{AvailabilityZone: rr.Spec.AvailabilityZone, IgnoreHosts: ignore}
	if e.kind == v1alpha1.DrainEntityKindVM {
		req.InstanceUUID = e.ref
	}
	return req
}

// entity is a single entity that must be moved off its source host.
type entity struct {
	kind       v1alpha1.DrainEntityKind
	ref        string
	sourceHost string
}

// entityCandidates pairs an entity with the filtered candidate target hosts
// discovered for it (best first), without any hold being taken, and the entity's
// size used as the big→small ordering tiebreaker.
type entityCandidates struct {
	entity
	candidates []string
	size       uint64
}

// orderEntities orders entities for reservation: fewest-options-first, then
// big→small. The candidate count is saturated at saturation, so an entity with
// many options does not keep sorting ahead of another with only slightly fewer;
// once an entity has "plenty" of options its exact count stops mattering and the
// size tiebreaker takes over.
func orderEntities(entities []entityCandidates, saturation int) {
	if saturation <= 0 {
		saturation = DefaultCandidateSaturation
	}
	slices.SortStableFunc(entities, func(a, b entityCandidates) int {
		if c := cmp.Compare(min(len(a.candidates), saturation), min(len(b.candidates), saturation)); c != 0 {
			return c
		}
		// Bigger entities first.
		return cmp.Compare(b.size, a.size)
	})
}
