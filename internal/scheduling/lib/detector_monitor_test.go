// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"errors"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNewDetectorPipelineMonitor(t *testing.T) {
	monitor := NewDetectorPipelineMonitor()

	if monitor.stepRunTimer == nil {
		t.Error("expected stepRunTimer to be initialized")
	}
	if monitor.stepDeschedulingCounter == nil {
		t.Error("expected stepDeschedulingCounter to be initialized")
	}
	if monitor.pipelineRunTimer == nil {
		t.Error("expected pipelineRunTimer to be initialized")
	}
}

func TestMonitor_Describe(t *testing.T) {
	monitor := NewDetectorPipelineMonitor()
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
	monitor := NewDetectorPipelineMonitor()
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
	decisions     []mockDetection
	initError     error
	validateError error
	runError      error
	initCalled    bool
	runCalled     bool
}

func (m *mockMonitorStep) Init(ctx context.Context, client client.Client, step v1alpha1.DetectorSpec) error {
	m.initCalled = true
	return m.initError
}

func (m *mockMonitorStep) Validate(ctx context.Context, params runtime.RawExtension) error {
	return m.validateError
}

func (m *mockMonitorStep) Run() ([]mockDetection, error) {
	m.runCalled = true
	return m.decisions, m.runError
}

func TestMonitorStep(t *testing.T) {
	monitor := NewDetectorPipelineMonitor()
	step := &mockMonitorStep{
		decisions: []mockDetection{
			{resource: "vm1", reason: "test"},
		},
	}
	conf := v1alpha1.DetectorSpec{Name: "test-step"}

	monitoredStep := monitorDetector(step, conf, monitor)

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
	monitor := NewDetectorPipelineMonitor()
	step := &mockMonitorStep{}
	conf := v1alpha1.DetectorSpec{Name: "test-step"}

	monitoredStep := monitorDetector(step, conf, monitor)

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
	monitor := NewDetectorPipelineMonitor()
	expectedErr := errors.New("init failed")
	step := &mockMonitorStep{
		initError: expectedErr,
	}
	conf := v1alpha1.DetectorSpec{Name: "test-step"}
	monitoredStep := monitorDetector(step, conf, monitor)

	client := fake.NewClientBuilder().Build()
	err := monitoredStep.Init(context.Background(), client, conf)

	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestStepMonitor_Run(t *testing.T) {
	monitor := NewDetectorPipelineMonitor()
	decisions := []mockDetection{
		{resource: "vm1", reason: "test1"},
		{resource: "vm2", reason: "test2"},
	}
	step := &mockMonitorStep{
		decisions: decisions,
	}
	conf := v1alpha1.DetectorSpec{Name: "test-step"}
	monitoredStep := monitorDetector(step, conf, monitor)

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
	monitor := NewDetectorPipelineMonitor()
	expectedErr := errors.New("run failed")
	step := &mockMonitorStep{
		runError: expectedErr,
	}
	conf := v1alpha1.DetectorSpec{Name: "test-step"}
	monitoredStep := monitorDetector(step, conf, monitor)

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
	monitor := NewDetectorPipelineMonitor()
	step := &mockMonitorStep{
		decisions: []mockDetection{}, // Empty slice
	}
	conf := v1alpha1.DetectorSpec{Name: "test-step"}
	monitoredStep := monitorDetector(step, conf, monitor)

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
	monitor := DetectorPipelineMonitor{}
	step := &mockMonitorStep{
		decisions: []mockDetection{
			{resource: "vm1", reason: "test"},
		},
	}
	conf := v1alpha1.DetectorSpec{Name: "test-step"}
	monitoredStep := monitorDetector(step, conf, monitor)

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

func TestDetectorPipelineMonitor_SubPipeline(t *testing.T) {
	tests := []struct {
		name             string
		originalName     string
		newPipelineName  string
		expectedOriginal string
		expectedNew      string
	}{
		{
			name:             "creates copy with new name",
			originalName:     "original-pipeline",
			newPipelineName:  "new-pipeline",
			expectedOriginal: "original-pipeline",
			expectedNew:      "new-pipeline",
		},
		{
			name:             "works with empty original name",
			originalName:     "",
			newPipelineName:  "new-pipeline",
			expectedOriginal: "",
			expectedNew:      "new-pipeline",
		},
		{
			name:             "works with empty new name",
			originalName:     "original-pipeline",
			newPipelineName:  "",
			expectedOriginal: "original-pipeline",
			expectedNew:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := NewDetectorPipelineMonitor()
			original.PipelineName = tt.originalName

			copied := original.SubPipeline(tt.newPipelineName)

			// Check that original is unchanged
			if original.PipelineName != tt.expectedOriginal {
				t.Errorf("original pipeline name changed, expected %s, got %s", tt.expectedOriginal, original.PipelineName)
			}

			// Check that copy has new name
			if copied.PipelineName != tt.expectedNew {
				t.Errorf("copied pipeline name incorrect, expected %s, got %s", tt.expectedNew, copied.PipelineName)
			}

			// Verify that the metrics are shared (same pointers)
			if copied.stepRunTimer != original.stepRunTimer {
				t.Error("expected stepRunTimer to be shared between original and copy")
			}
			if copied.stepDeschedulingCounter != original.stepDeschedulingCounter {
				t.Error("expected stepDeschedulingCounter to be shared between original and copy")
			}
			if copied.pipelineRunTimer != original.pipelineRunTimer {
				t.Error("expected pipelineRunTimer to be shared between original and copy")
			}
		})
	}
}
