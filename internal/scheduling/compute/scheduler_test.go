// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

// fakePipeline is a stub lib.FilterWeigherPipeline[SchedulerRequest] that records
// the request it was run with and returns a configurable result/error. The run
// function, if set, is invoked inside Run (used to observe lock behavior).
type fakePipeline struct {
	result  SchedulerResult
	err     error
	onRun   func(SchedulerRequest)
	lastReq SchedulerRequest
}

func (p *fakePipeline) Run(req SchedulerRequest) (SchedulerResult, error) {
	p.lastReq = req
	if p.onRun != nil {
		p.onRun(req)
	}
	return p.result, p.err
}

// fakeProvider implements PipelineProvider. A pipeline is returned only for the
// configured name.
type fakeProvider struct {
	name     string
	pipeline lib.FilterWeigherPipeline[SchedulerRequest]
}

func (f *fakeProvider) GetPipeline(name string) (lib.FilterWeigherPipeline[SchedulerRequest], bool) {
	if name != f.name || f.pipeline == nil {
		return nil, false
	}
	return f.pipeline, true
}

// fakeGatherer implements CandidateGatherer, recording whether it was called.
type fakeGatherer struct {
	called bool
	err    error
}

func (g *fakeGatherer) MutateWithAllCandidates(_ context.Context, req *SchedulerRequest) error {
	g.called = true
	if g.err != nil {
		return g.err
	}
	req.Hosts = []Host{{ComputeHost: "gathered", HypervisorHostname: "gathered"}}
	return nil
}

// newTestScheduler builds a Scheduler whose only ready pipeline is named "p".
func newTestScheduler(p *fakePipeline, g CandidateGatherer) *Scheduler {
	return NewScheduler(&fakeProvider{name: "p", pipeline: p}, g)
}

func TestRunPipelineNotFound(t *testing.T) {
	s := newTestScheduler(&fakePipeline{}, &fakeGatherer{})
	_, err := s.Run(context.Background(), SchedulerRequest{Pipeline: "missing"}, nil, nil)
	if err == nil {
		t.Fatal("expected error for missing pipeline, got nil")
	}
}

func TestRunPreThenPost(t *testing.T) {
	s := newTestScheduler(&fakePipeline{}, &fakeGatherer{})
	var order []string
	pre := func(_ context.Context, _ *SchedulerRequest) error {
		order = append(order, "pre")
		return nil
	}
	post := func(_ context.Context, _ SchedulerRequest, _ SchedulerResult) error {
		order = append(order, "post")
		return nil
	}
	if _, err := s.Run(context.Background(), SchedulerRequest{Pipeline: "p"}, pre, post); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 2 || order[0] != "pre" || order[1] != "post" {
		t.Fatalf("hooks ran out of order: %v", order)
	}
}

func TestRunNilHooks(t *testing.T) {
	s := newTestScheduler(&fakePipeline{}, &fakeGatherer{})
	if _, err := s.Run(context.Background(), SchedulerRequest{Pipeline: "p"}, nil, nil); err != nil {
		t.Fatalf("nil hooks should be a no-op, got error: %v", err)
	}
}

func TestRunPostHookErrorAborts(t *testing.T) {
	s := newTestScheduler(&fakePipeline{}, &fakeGatherer{})
	boom := errors.New("boom")
	post := func(_ context.Context, _ SchedulerRequest, _ SchedulerResult) error { return boom }
	_, err := s.Run(context.Background(), SchedulerRequest{Pipeline: "p"}, nil, post)
	if !errors.Is(err, boom) {
		t.Fatalf("expected boom error, got %v", err)
	}
}

func TestRunPipelineErrorSkipsPostHook(t *testing.T) {
	boom := errors.New("pipeline failed")
	s := newTestScheduler(&fakePipeline{err: boom}, &fakeGatherer{})
	var ran bool
	post := func(_ context.Context, _ SchedulerRequest, _ SchedulerResult) error { ran = true; return nil }
	_, err := s.Run(context.Background(), SchedulerRequest{Pipeline: "p"}, nil, post)
	if !errors.Is(err, boom) {
		t.Fatalf("expected pipeline error, got %v", err)
	}
	if ran {
		t.Fatal("post-hook must not run when the pipeline errors")
	}
}

func TestRunPreHookRunsBeforePipelineAndMutates(t *testing.T) {
	var sawHostInPipeline string
	p := &fakePipeline{onRun: func(r SchedulerRequest) {
		if len(r.Hosts) > 0 {
			sawHostInPipeline = r.Hosts[0].ComputeHost
		}
	}}
	s := newTestScheduler(p, &fakeGatherer{})
	pre := func(_ context.Context, r *SchedulerRequest) error {
		r.Hosts = []Host{{ComputeHost: "from-prehook"}}
		return nil
	}
	if _, err := s.Run(context.Background(), SchedulerRequest{Pipeline: "p"}, pre, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sawHostInPipeline != "from-prehook" {
		t.Fatalf("pre-hook mutation not visible to pipeline: got %q", sawHostInPipeline)
	}
}

func TestRunPreHookErrorAbortsBeforePipeline(t *testing.T) {
	var pipelineRan bool
	p := &fakePipeline{onRun: func(SchedulerRequest) { pipelineRan = true }}
	s := newTestScheduler(p, &fakeGatherer{})
	boom := errors.New("prehook boom")
	pre := func(_ context.Context, _ *SchedulerRequest) error { return boom }
	_, err := s.Run(context.Background(), SchedulerRequest{Pipeline: "p"}, pre, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("expected prehook error, got %v", err)
	}
	if pipelineRan {
		t.Fatal("pipeline must not run after a pre-hook error")
	}
}

func TestRunGathersCandidatesWhenRequested(t *testing.T) {
	g := &fakeGatherer{}
	p := &fakePipeline{}
	s := newTestScheduler(p, g)
	req := SchedulerRequest{Pipeline: "p", GatherAllCandidates: true}
	if _, err := s.Run(context.Background(), req, nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !g.called {
		t.Fatal("expected gatherer to be called when GatherAllCandidates is set")
	}
	if len(p.lastReq.Hosts) != 1 || p.lastReq.Hosts[0].ComputeHost != "gathered" {
		t.Fatalf("pipeline did not see gathered candidates: %+v", p.lastReq.Hosts)
	}
}

func TestRunDoesNotGatherWithoutGatherAllCandidates(t *testing.T) {
	g := &fakeGatherer{}
	s := newTestScheduler(&fakePipeline{}, g)
	if _, err := s.Run(context.Background(), SchedulerRequest{Pipeline: "p"}, nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if g.called {
		t.Fatal("gatherer should not be called without GatherAllCandidates")
	}
}

// TestRunHoldsLockAcrossPostHook verifies that a second Run blocks until the
// first Run's post-hook completes — i.e. the lock spans the whole run, not just
// the pipeline.
func TestRunHoldsLockAcrossPostHook(t *testing.T) {
	s := newTestScheduler(&fakePipeline{}, &fakeGatherer{})

	firstInPost := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondDone := make(chan struct{})

	var slowPost PostHook = func(_ context.Context, _ SchedulerRequest, _ SchedulerResult) error {
		close(firstInPost)
		<-releaseFirst
		return nil
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := s.Run(context.Background(), SchedulerRequest{Pipeline: "p"}, nil, slowPost); err != nil {
			t.Errorf("first Run: %v", err)
		}
	}()

	<-firstInPost // first Run is parked inside its post-hook, still holding the lock.

	go func() {
		if _, err := s.Run(context.Background(), SchedulerRequest{Pipeline: "p"}, nil, nil); err != nil {
			t.Errorf("second Run: %v", err)
		}
		close(secondDone)
	}()

	// The second Run must be blocked on the lock held by the first.
	select {
	case <-secondDone:
		t.Fatal("second Run completed while the first still held the lock")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseFirst) // let the first Run finish and release the lock.
	wg.Wait()
	<-secondDone // the second Run now proceeds.
}

// TestRunWithoutLockDoesNotBlock verifies that RunWithoutLock proceeds even while
// a locked Run holds the lock inside its post-hook.
func TestRunWithoutLockDoesNotBlock(t *testing.T) {
	s := newTestScheduler(&fakePipeline{}, &fakeGatherer{})

	firstInPost := make(chan struct{})
	releaseFirst := make(chan struct{})
	var slowPost PostHook = func(_ context.Context, _ SchedulerRequest, _ SchedulerResult) error {
		close(firstInPost)
		<-releaseFirst
		return nil
	}

	go func() {
		if _, err := s.Run(context.Background(), SchedulerRequest{Pipeline: "p"}, nil, slowPost); err != nil {
			t.Errorf("locked Run: %v", err)
		}
	}()
	<-firstInPost // locked Run holds the lock.

	done := make(chan struct{})
	go func() {
		if _, err := s.RunWithoutLock(context.Background(), SchedulerRequest{Pipeline: "p"}, nil, nil); err != nil {
			t.Errorf("RunWithoutLock: %v", err)
		}
		close(done)
	}()

	select {
	case <-done:
		// Expected: lock-free run completes without waiting for the lock.
	case <-time.After(time.Second):
		t.Fatal("RunWithoutLock blocked on the scheduling lock")
	}
	close(releaseFirst)
}
