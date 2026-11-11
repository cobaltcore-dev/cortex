// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"context"
	"fmt"
	"sync"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	ctrl "sigs.k8s.io/controller-runtime"
)

// PipelineProvider supplies initialized pipelines by name. The Nova
// filter-weigher pipeline controller satisfies this interface via its embedded
// lib.BasePipelineController.
type PipelineProvider interface {
	// GetPipeline returns the initialized pipeline for the given name, if ready.
	GetPipeline(name string) (lib.FilterWeigherPipeline[PlacementRequest], bool)
}

// CandidateGatherer gathers all placement candidates for a request when the
// pipeline is configured to ignore the caller's preselection.
type CandidateGatherer interface {
	// MutateWithAllCandidates overrides the request's hosts and weights with all
	// available placement candidates.
	MutateWithAllCandidates(ctx context.Context, request *PlacementRequest) error
}

// PreHook is a caller-provided hook executed under the scheduling lock before
// the pipeline runs. It may mutate the request (e.g. to create a pessimistic
// in-flight reservation and narrow the candidate set). Returning an error aborts
// the run before the pipeline executes. A nil PreHook is a no-op.
type PreHook func(ctx context.Context, req *PlacementRequest) error

// PostHook is a caller-provided side effect executed after the pipeline run,
// under the scheduling lock (e.g. upserting the VM resource, recording the
// decision). It receives the typed pipeline result. Returning an error aborts
// the run; non-fatal side effects should log and return nil so the placement
// decision still stands. A nil PostHook is a no-op.
type PostHook func(ctx context.Context, req PlacementRequest, result PlacementResult) error

// Placement is the single in-process owner of Nova placement decisions. It holds
// the one shared scheduling lock and runs the pipeline plus a caller-provided
// pre-hook and post-hook. It owns no side-effect policy itself — the caller
// decides, via the hooks it passes, what happens before and after the run.
type Placement struct {
	// mu is the single shared scheduling lock, held across the pre-hook, the
	// pipeline run, and the post-hook in Run. RunWithoutLock bypasses it.
	mu sync.Mutex

	// pipelines resolves pipelines + their config by name.
	pipelines PipelineProvider
	// gatherer gathers all candidates when a pipeline ignores preselection.
	gatherer CandidateGatherer
}

// NewPlacement builds a Placement over the given pipeline provider and candidate
// gatherer.
func NewPlacement(pipelines PipelineProvider, gatherer CandidateGatherer) *Placement {
	return &Placement{pipelines: pipelines, gatherer: gatherer}
}

// Run executes the pipeline and hooks while holding the shared scheduling lock
// across the whole operation, so concurrent runs observe a consistent view of
// shared scheduling state. The pre-hook runs before the pipeline; the post-hook
// after. Either hook may be nil. Use this for any run that mutates shared state.
func (s *Placement) Run(
	ctx context.Context,
	req PlacementRequest,
	pre PreHook,
	post PostHook,
) (PlacementResult, error) {

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.run(ctx, req, pre, post)
}

// RunWithoutLock executes the pipeline and hooks WITHOUT acquiring the shared
// lock. Use this only for read-only runs (e.g. capacity probes) that do not
// mutate shared scheduling state and can safely run concurrently with a locked
// run. Either hook may be nil.
func (s *Placement) RunWithoutLock(
	ctx context.Context,
	req PlacementRequest,
	pre PreHook,
	post PostHook,
) (PlacementResult, error) {

	return s.run(ctx, req, pre, post)
}

// run is the shared body of Run and RunWithoutLock. The caller is responsible
// for holding (or deliberately not holding) the lock.
func (s *Placement) run(
	ctx context.Context,
	req PlacementRequest,
	pre PreHook,
	post PostHook,
) (PlacementResult, error) {

	log := ctrl.LoggerFrom(ctx)

	pipeline, ok := s.pipelines.GetPipeline(req.Pipeline)
	if !ok {
		return PlacementResult{}, fmt.Errorf("pipeline not found or not ready: %q", req.Pipeline)
	}

	// The pre-hook runs before the pipeline and may mutate the request.
	if pre != nil {
		if err := pre(ctx, &req); err != nil {
			return PlacementResult{}, err
		}
	}

	// Apply defaults after the pre-hook, which may have populated load-bearing
	// fields.
	req.ApplyDefaults()

	// When the request opts into gathering all candidates, override the hosts and
	// weights with every available placement candidate before filtering.
	if req.GatherAllCandidates {
		log.V(1).Info("gathering all placement candidates before filtering")
		if err := s.gatherer.MutateWithAllCandidates(ctx, &req); err != nil {
			return PlacementResult{}, fmt.Errorf("failed to gather all placement candidates: %w", err)
		}
		log.V(1).Info("gathered all placement candidates", "numHosts", len(req.Hosts))
	}

	result, runErr := pipeline.Run(req)
	if runErr != nil {
		return PlacementResult{}, runErr
	}

	// The post-hook runs after a successful pipeline run, under the lock.
	if post != nil {
		if err := post(ctx, req, result); err != nil {
			return result, err
		}
	}
	return result, nil
}
