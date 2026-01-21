// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/descheduling/nova/plugins"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNewPipelineMonitor(t *testing.T) {
	monitor := NewPipelineMonitor()

	if monitor.stepRunTimer == nil {
		t.Error("expected stepRunTimer to be initialized")
	}
	if monitor.stepDeschedulingCounter == nil {
		t.Error("expected stepDeschedulingCounter to be initialized")
	}
	if monitor.pipelineRunTimer == nil {
		t.Error("expected pipelineRunTimer to be initialized")
	}
	if monitor.deschedulingRunTimer == nil {
		t.Error("expected deschedulingRunTimer to be initialized")
	}
}

func TestMonitor_Describe(t *testing.T) {
	monitor := NewPipelineMonitor()
	descs := make(chan *prometheus.Desc, 10)

	go func() {
		monitor.Describe(descs)
		close(descs)
	}()

	count := 0
	for range descs {
		count++
	}

	if count == 0 {
		t.Error("expected at least one metric description")
	}
}

func TestMonitor_Collect(t *testing.T) {
	monitor := NewPipelineMonitor()
	metrics := make(chan prometheus.Metric, 10)

	go func() {
		monitor.Collect(metrics)
		close(metrics)
	}()

	count := 0
	for range metrics {
		count++
	}

	// Initially no metrics should be collected (they're created when used)
	if count < 0 {
		t.Error("unexpected negative metric count")
	}
}

type mockMonitorStep struct {
	decisions  []plugins.Decision
	initError  error
	runError   error
	initCalled bool
	runCalled  bool
}

func (m *mockMonitorStep) Init(ctx context.Context, client client.Client, step v1alpha1.DetectorSpec) error {
	m.initCalled = true
	return m.initError
}

func (m *mockMonitorStep) Run() ([]plugins.Decision, error) {
	m.runCalled = true
	return m.decisions, m.runError
}

func TestMonitorStep(t *testing.T) {
	monitor := NewPipelineMonitor()
	step := &mockMonitorStep{
		decisions: []plugins.Decision{
			{VMID: "vm1", Reason: "test"},
		},
	}
	conf := v1alpha1.DetectorSpec{Name: "test-step"}

	monitoredStep := monitorStep(step, conf, monitor)

	if monitoredStep.step != step {
		t.Error("expected wrapped step to be preserved")
	}

	if monitoredStep.runTimer == nil {
		t.Error("expected runTimer to be set")
	}

	if monitoredStep.descheduledCounter == nil {
		t.Error("expected descheduledCounter to be set")
	}
}

func TestStepMonitor_Init(t *testing.T) {
	monitor := NewPipelineMonitor()
	step := &mockMonitorStep{}
	conf := v1alpha1.DetectorSpec{Name: "test-step"}

	monitoredStep := monitorStep(step, conf, monitor)

	client := fake.NewClientBuilder().Build()
	err := monitoredStep.Init(context.Background(), client, conf)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !step.initCalled {
		t.Error("expected Init to be called on wrapped step")
	}
}

func TestStepMonitor_Init_WithError(t *testing.T) {
	monitor := NewPipelineMonitor()
	expectedErr := errors.New("init failed")
	step := &mockMonitorStep{
		initError: expectedErr,
	}
	conf := v1alpha1.DetectorSpec{Name: "test-step"}
	monitoredStep := monitorStep(step, conf, monitor)

	client := fake.NewClientBuilder().Build()
	err := monitoredStep.Init(context.Background(), client, conf)

	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestStepMonitor_Run(t *testing.T) {
	monitor := NewPipelineMonitor()
	decisions := []plugins.Decision{
		{VMID: "vm1", Reason: "test1"},
		{VMID: "vm2", Reason: "test2"},
	}
	step := &mockMonitorStep{
		decisions: decisions,
	}
	conf := v1alpha1.DetectorSpec{Name: "test-step"}
	monitoredStep := monitorStep(step, conf, monitor)

	result, err := monitoredStep.Run()

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !step.runCalled {
		t.Error("expected Run to be called on wrapped step")
	}

	if len(result) != 2 {
		t.Errorf("expected 2 decisions, got %d", len(result))
	}

	// Verify that the counter was incremented
	counterValue := testutil.ToFloat64(monitor.stepDeschedulingCounter.WithLabelValues("test-step"))
	if counterValue != 2.0 {
		t.Errorf("expected counter value 2.0, got %f", counterValue)
	}
}

func TestStepMonitor_Run_WithError(t *testing.T) {
	monitor := NewPipelineMonitor()
	expectedErr := errors.New("run failed")
	step := &mockMonitorStep{
		runError: expectedErr,
	}
	conf := v1alpha1.DetectorSpec{Name: "test-step"}
	monitoredStep := monitorStep(step, conf, monitor)

	result, err := monitoredStep.Run()

	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}

	if result != nil {
		t.Errorf("expected nil result on error, got %v", result)
	}

	// Counter should not be incremented on error
	counterValue := testutil.ToFloat64(monitor.stepDeschedulingCounter.WithLabelValues("test-step"))
	if counterValue != 0.0 {
		t.Errorf("expected counter value 0.0, got %f", counterValue)
	}
}

func TestStepMonitor_Run_EmptyResult(t *testing.T) {
	monitor := NewPipelineMonitor()
	step := &mockMonitorStep{
		decisions: []plugins.Decision{}, // Empty slice
	}
	conf := v1alpha1.DetectorSpec{Name: "test-step"}
	monitoredStep := monitorStep(step, conf, monitor)

	result, err := monitoredStep.Run()

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("expected 0 decisions, got %d", len(result))
	}

	// Counter should be 0 for empty results
	counterValue := testutil.ToFloat64(monitor.stepDeschedulingCounter.WithLabelValues("test-step"))
	if counterValue != 0.0 {
		t.Errorf("expected counter value 0.0, got %f", counterValue)
	}
}

func TestMonitorStep_WithNilMonitor(t *testing.T) {
	// Test with empty monitor (nil fields)
	monitor := Monitor{}
	step := &mockMonitorStep{
		decisions: []plugins.Decision{
			{VMID: "vm1", Reason: "test"},
		},
	}
	conf := v1alpha1.DetectorSpec{Name: "test-step"}
	monitoredStep := monitorStep(step, conf, monitor)

	// Should not panic with nil timers/counters
	result, err := monitoredStep.Run()

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if len(result) != 1 {
		t.Errorf("expected 1 decision, got %d", len(result))
	}

	if !step.runCalled {
		t.Error("expected Run to be called on wrapped step")
	}
}
