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

func TestMonitorFilter(t *testing.T) {
	monitor := FilterWeigherPipelineMonitor{
		PipelineName: "test-pipeline",
	}

	mockFilter := &mockFilter[mockFilterWeigherPipelineRequest]{
		InitFunc: func(ctx context.Context, cl client.Client, step v1alpha1.FilterSpec) error {
			return nil
		},
		RunFunc: func(traceLog *slog.Logger, request mockFilterWeigherPipelineRequest) (*FilterWeigherPipelineStepResult, error) {
			return &FilterWeigherPipelineStepResult{
				Activations: map[string]float64{"host1": 0.5, "host2": 1.0},
			}, nil
		},
	}

	fm := monitorFilter(mockFilter, "test-filter", monitor)
	if fm == nil {
		t.Fatal("expected filter monitor, got nil")
	}
	if fm.filter == nil {
		t.Error("expected filter to be set")
	}
	if fm.monitor == nil {
		t.Error("expected monitor to be set")
	}
	if fm.monitor.stepName != "test-filter" {
		t.Errorf("expected step name 'test-filter', got '%s'", fm.monitor.stepName)
	}
}

func TestFilterMonitor_Init(t *testing.T) {
	initCalled := false
	mockFilter := &mockFilter[mockFilterWeigherPipelineRequest]{
		InitFunc: func(ctx context.Context, cl client.Client, step v1alpha1.FilterSpec) error {
			initCalled = true
			return nil
		},
	}

	monitor := FilterWeigherPipelineMonitor{
		PipelineName: "test-pipeline",
	}
	fm := monitorFilter(mockFilter, "test-filter", monitor)

	scheme := runtime.NewScheme()
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	err := fm.Init(t.Context(), cl, v1alpha1.FilterSpec{
		Name: "test-filter",
		Params: runtime.RawExtension{
			Raw: []byte(`{}`),
		},
	})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if !initCalled {
		t.Error("expected Init to be called on wrapped filter")
	}
}

func TestFilterMonitor_Run(t *testing.T) {
	runCalled := false
	mockFilter := &mockFilter[mockFilterWeigherPipelineRequest]{
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
	fm := monitorFilter(mockFilter, "test-filter", monitor)
	// Manually set monitors for testing
	fm.monitor.runTimer = runTimer
	fm.monitor.removedSubjectsObserver = removedSubjectsObserver

	request := mockFilterWeigherPipelineRequest{
		Subjects: []string{"host1", "host2", "host3"},
		Weights:  map[string]float64{"host1": 0.1, "host2": 0.2, "host3": 0.3},
	}

	result, err := fm.Run(slog.Default(), request)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if !runCalled {
		t.Error("expected Run to be called on wrapped filter")
	}
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	if len(result.Activations) != 2 {
		t.Errorf("expected 2 activations, got %d", len(result.Activations))
	}
}
