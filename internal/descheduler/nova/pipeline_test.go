// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

// testExecutor allows us to intercept Deschedule calls for testing.
type testExecutor struct {
	called chan []string
	cancel context.CancelFunc
}

func (e *testExecutor) Deschedule(ctx context.Context, vmIDs []string) error {
	e.called <- vmIDs
	if e.cancel != nil {
		e.cancel()
	}
	return nil
}

type testCycleDetector struct {
	mockCycleDetector
	called chan []string
}

func (d *testCycleDetector) Filter(ctx context.Context, vmIDs []string) ([]string, error) {
	d.called <- vmIDs
	return vmIDs, nil // For simplicity, return the same VMs
}

func TestPipeline_DeschedulePeriodically(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	descheduleCalled := make(chan []string, 1)
	exec := &testExecutor{called: descheduleCalled, cancel: cancel}
	cycleDetector := &testCycleDetector{called: make(chan []string, 1)}

	step := &mockStep{Name: "step1", Decisions: []string{"vm1", "vm2"}}
	p := &Pipeline{
		steps:         []Step{step},
		monitor:       Monitor{},
		executor:      exec,
		cycleDetector: cycleDetector,
	}

	go p.DeschedulePeriodically(ctx)

	select {
	case vms := <-descheduleCalled:
		if len(vms) != 2 || (vms[0] != "vm1" && vms[1] != "vm2" && vms[0] != "vm2" && vms[1] != "vm1") {
			t.Errorf("unexpected vms to deschedule: %v", vms)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Deschedule was not called in time")
	}
	select {
	case vms := <-cycleDetector.called:
		if len(vms) != 2 || (vms[0] != "vm1" && vms[1] != "vm2" && vms[0] != "vm2" && vms[1] != "vm1") {
			t.Errorf("unexpected vms in cycle detector: %v", vms)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Cycle detector was not called in time")
	}
}

func TestPipeline_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	config := conf.DeschedulerConfig{
		Nova: conf.NovaDeschedulerConfig{
			Plugins: []conf.DeschedulerStepConfig{{Name: "step1", Options: conf.RawOpts{}}},
		},
	}
	supportedSteps := []Step{
		&mockStep{Name: "step1"},
	}
	p := &Pipeline{monitor: Monitor{}, executor: &mockExecutor{}, cycleDetector: &mockCycleDetector{}, novaAPI: &mockNovaAPI{}}
	p.Init(supportedSteps, t.Context(), testDB, config)
	if len(p.steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(p.steps))
	}
	if p.steps[0].GetName() != "step1" {
		t.Errorf("expected step name 'step1', got '%s'", p.steps[0].GetName())
	}
}

func TestPipeline_run(t *testing.T) {
	step1 := &mockStep{Name: "step1", Decisions: []string{"vm1", "vm2"}}
	step2 := &mockStep{Name: "step2", Decisions: []string{"vm2", "vm3"}}
	p := &Pipeline{steps: []Step{step1, step2}, monitor: Monitor{}, cycleDetector: &mockCycleDetector{}}

	results := p.run()
	if len(results) != 2 {
		t.Fatalf("expected 2 step results, got %d", len(results))
	}
	if _, ok := results["step1"]; !ok {
		t.Errorf("expected result for step1")
	}
	if _, ok := results["step2"]; !ok {
		t.Errorf("expected result for step2")
	}
	if results["step1"][0] != "vm1" || results["step2"][1] != "vm3" {
		t.Errorf("unexpected decisions: %v", results)
	}
}

func TestPipeline_run_stepSkipped(t *testing.T) {
	step := &mockStep{Name: "step1", RunErr: ErrStepSkipped}
	p := &Pipeline{steps: []Step{step}, monitor: Monitor{}, cycleDetector: &mockCycleDetector{}}

	results := p.run()
	if len(results) != 0 {
		t.Errorf("expected 0 results for skipped step, got %d", len(results))
	}
}

func TestPipeline_run_stepError(t *testing.T) {
	step := &mockStep{Name: "step1", RunErr: errors.New("fail")}
	p := &Pipeline{steps: []Step{step}, monitor: Monitor{}, cycleDetector: &mockCycleDetector{}}

	results := p.run()
	if len(results) != 0 {
		t.Errorf("expected 0 results for error step, got %d", len(results))
	}
}

func TestPipeline_deduplicate(t *testing.T) {
	p := &Pipeline{cycleDetector: &mockCycleDetector{}}
	decisionsByStep := map[string][]string{
		"step1": {"vm1", "vm2"},
		"step2": {"vm2", "vm3"},
	}
	result := p.deduplicate(decisionsByStep)
	vmSet := make(map[string]struct{})
	for _, vm := range result {
		vmSet[vm] = struct{}{}
	}
	if len(vmSet) != 3 {
		t.Errorf("expected 3 unique vms, got %d", len(vmSet))
	}
	for _, vm := range []string{"vm1", "vm2", "vm3"} {
		if _, ok := vmSet[vm]; !ok {
			t.Errorf("missing vm %s", vm)
		}
	}
}
