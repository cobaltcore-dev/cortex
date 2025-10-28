// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/lib/db"
	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/manila"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/lib"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestNewPipeline(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	monitor := lib.PipelineMonitor{}

	tests := []struct {
		name        string
		steps       []v1alpha1.Step
		expectError bool
	}{
		{
			name:        "empty steps pipeline",
			steps:       []v1alpha1.Step{},
			expectError: false,
		},
		{
			name: "pipeline with supported netapp step",
			steps: []v1alpha1.Step{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cpu-usage-balancing-step",
					},
					Spec: v1alpha1.StepSpec{
						Type: v1alpha1.StepTypeWeigher,
						Impl: "netapp_cpu_usage_balancing",
						Opts: runtime.RawExtension{
							Raw: []byte(`{"AvgCPUUsageLowerBound": 0, "AvgCPUUsageUpperBound": 90, "MaxCPUUsageLowerBound": 0, "MaxCPUUsageUpperBound": 100}`),
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "pipeline with unsupported step",
			steps: []v1alpha1.Step{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "unsupported-step",
					},
					Spec: v1alpha1.StepSpec{
						Type: v1alpha1.StepTypeFilter,
						Impl: "unsupported-filter",
					},
				},
			},
			expectError: true,
		},
		{
			name: "pipeline with multiple steps",
			steps: []v1alpha1.Step{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cpu-usage-balancing-step-1",
					},
					Spec: v1alpha1.StepSpec{
						Type: v1alpha1.StepTypeWeigher,
						Impl: "netapp_cpu_usage_balancing",
						Opts: runtime.RawExtension{
							Raw: []byte(`{"AvgCPUUsageLowerBound": 0, "AvgCPUUsageUpperBound": 80, "MaxCPUUsageLowerBound": 0, "MaxCPUUsageUpperBound": 100}`),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cpu-usage-balancing-step-2",
					},
					Spec: v1alpha1.StepSpec{
						Type: v1alpha1.StepTypeWeigher,
						Impl: "netapp_cpu_usage_balancing",
						Opts: runtime.RawExtension{
							Raw: []byte(`{"AvgCPUUsageLowerBound": 0, "AvgCPUUsageUpperBound": 100, "MaxCPUUsageLowerBound": 0, "MaxCPUUsageUpperBound": 95}`),
						},
					},
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipeline, err := NewPipeline(tt.steps, testDB, monitor)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
			if !tt.expectError && pipeline == nil {
				t.Error("Expected pipeline but got nil")
			}
		})
	}
}

func TestPipelineRun(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	monitor := lib.PipelineMonitor{}

	// Create a pipeline with no steps for basic testing
	pipeline, err := NewPipeline([]v1alpha1.Step{}, testDB, monitor)
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}

	tests := []struct {
		name    string
		request api.ExternalSchedulerRequest
	}{
		{
			name: "basic manila request",
			request: api.ExternalSchedulerRequest{
				Spec: map[string]any{
					"share_id": "test-share-id",
					"size":     10,
				},
				Context: api.ManilaRequestContext{
					ProjectID:       "test-project",
					UserID:          "test-user",
					RequestID:       "req-123",
					GlobalRequestID: "global-req-123",
				},
				Hosts: []api.ExternalSchedulerHost{
					{ShareHost: "manila-share-1@backend1"},
					{ShareHost: "manila-share-2@backend2"},
				},
				Weights: map[string]float64{
					"manila-share-1@backend1": 1.0,
					"manila-share-2@backend2": 0.5,
				},
				Pipeline: "test-pipeline",
			},
		},
		{
			name: "manila request with single host",
			request: api.ExternalSchedulerRequest{
				Spec: map[string]any{
					"share_id": "single-share-id",
					"size":     5,
				},
				Context: api.ManilaRequestContext{
					ProjectID:       "single-project",
					UserID:          "single-user",
					RequestID:       "single-req-123",
					GlobalRequestID: "single-global-req-123",
				},
				Hosts: []api.ExternalSchedulerHost{
					{ShareHost: "manila-share-single@backend1"},
				},
				Weights: map[string]float64{
					"manila-share-single@backend1": 1.0,
				},
				Pipeline: "single-pipeline",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := pipeline.Run(tt.request)

			if err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if len(result.OrderedHosts) == 0 {
				t.Error("Expected ordered hosts but got empty slice")
			}

			if result.TargetHost == nil {
				t.Error("Expected target host but got nil")
			}

			// Verify all hosts from request are in the result
			expectedHosts := make(map[string]bool)
			for _, host := range tt.request.Hosts {
				expectedHosts[host.ShareHost] = true
			}

			for _, host := range result.OrderedHosts {
				if !expectedHosts[host] {
					t.Errorf("Unexpected host in result: %s", host)
				}
			}

			// Verify target host is from the ordered hosts
			targetFound := false
			for _, host := range result.OrderedHosts {
				if *result.TargetHost == host {
					targetFound = true
					break
				}
			}
			if !targetFound {
				t.Errorf("Target host %s not found in ordered hosts", *result.TargetHost)
			}
		})
	}
}

func TestSupportedSteps(t *testing.T) {
	// Test that all supported steps can be instantiated
	for stepName, stepFactory := range supportedSteps {
		t.Run(stepName, func(t *testing.T) {
			step := stepFactory()
			if step == nil {
				t.Errorf("Step factory for %s returned nil", stepName)
			}

			if step.GetName() != stepName {
				t.Errorf("Expected step name %s but got %s", stepName, step.GetName())
			}
		})
	}
}
