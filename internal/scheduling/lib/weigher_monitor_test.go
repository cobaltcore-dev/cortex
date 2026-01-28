// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestMonitorWeigher(t *testing.T) {
	monitor := FilterWeigherPipelineMonitor{
		PipelineName: "test-pipeline",
	}

	mockWeigher := &mockWeigher[mockFilterWeigherPipelineRequest]{
		InitFunc: func(ctx context.Context, cl client.Client, step v1alpha1.WeigherSpec) error {
			return nil
		},
		RunFunc: func(traceLog *slog.Logger, request mockFilterWeigherPipelineRequest) (*FilterWeigherPipelineStepResult, error) {
			return &FilterWeigherPipelineStepResult{
				Activations: map[string]float64{"host1": 0.5, "host2": 1.0},
			}, nil
		},
	}

	wm := monitorWeigher(mockWeigher, "test-weigher", monitor)
	if wm == nil {
		t.Fatal("expected weigher monitor, got nil")
	}
	if wm.weigher == nil {
		t.Error("expected weigher to be set")
	}
	if wm.monitor == nil {
		t.Error("expected monitor to be set")
	}
	if wm.monitor.stepName != "test-weigher" {
		t.Errorf("expected step name 'test-weigher', got '%s'", wm.monitor.stepName)
	}
}

func TestWeigherMonitor_Init(t *testing.T) {
	initCalled := false
	mockWeigher := &mockWeigher[mockFilterWeigherPipelineRequest]{
		InitFunc: func(ctx context.Context, cl client.Client, step v1alpha1.WeigherSpec) error {
			initCalled = true
			return nil
		},
	}

	monitor := FilterWeigherPipelineMonitor{
		PipelineName: "test-pipeline",
	}
	wm := monitorWeigher(mockWeigher, "test-weigher", monitor)

	scheme := runtime.NewScheme()
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	err := wm.Init(t.Context(), cl, v1alpha1.WeigherSpec{
		Name: "test-weigher",
		Params: runtime.RawExtension{
			Raw: []byte(`{}`),
		},
	})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if !initCalled {
		t.Error("expected Init to be called on wrapped weigher")
	}
}

func TestWeigherMonitor_Run(t *testing.T) {
	runCalled := false
	mockWeigher := &mockWeigher[mockFilterWeigherPipelineRequest]{
		RunFunc: func(traceLog *slog.Logger, request mockFilterWeigherPipelineRequest) (*FilterWeigherPipelineStepResult, error) {
			runCalled = true
			return &FilterWeigherPipelineStepResult{
				Activations: map[string]float64{"host1": 0.5, "host2": 1.0},
			}, nil
		},
	}

	runTimer := &mockObserver{}
	removedSubjectsObserver := &mockObserver{}
	monitor := FilterWeigherPipelineMonitor{
		PipelineName: "test-pipeline",
	}
	wm := monitorWeigher(mockWeigher, "test-weigher", monitor)
	// Manually set monitors for testing
	wm.monitor.runTimer = runTimer
	wm.monitor.removedSubjectsObserver = removedSubjectsObserver

	request := mockFilterWeigherPipelineRequest{
		Subjects: []string{"host1", "host2", "host3"},
		Weights:  map[string]float64{"host1": 0.1, "host2": 0.2, "host3": 0.3},
	}

	result, err := wm.Run(slog.Default(), request)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if !runCalled {
		t.Error("expected Run to be called on wrapped weigher")
	}
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	if len(result.Activations) != 2 {
		t.Errorf("expected 2 activations, got %d", len(result.Activations))
	}
}
