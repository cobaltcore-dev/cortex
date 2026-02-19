// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// mockDetectorStep implements Detector[mockDetection]
type mockDetectorStep struct {
	decisions   []mockDetection
	initErr     error
	validateErr error
	runErr      error
}

func (m *mockDetectorStep) Init(ctx context.Context, client client.Client, step v1alpha1.DetectorSpec) error {
	return m.initErr
}

func (m *mockDetectorStep) Validate(ctx context.Context, params runtime.RawExtension) error {
	return m.validateErr
}

func (m *mockDetectorStep) Run() ([]mockDetection, error) {
	return m.decisions, m.runErr
}

func TestDetectorPipeline_Init(t *testing.T) {
	tests := []struct {
		name               string
		confedSteps        []v1alpha1.DetectorSpec
		supportedSteps     map[string]Detector[mockDetection]
		expectNonCritical  bool
		expectedStepsCount int
	}{
		{
			name: "successful init with one step",
			confedSteps: []v1alpha1.DetectorSpec{
				{Name: "step1", Params: nil},
			},
			supportedSteps: map[string]Detector[mockDetection]{
				"step1": &mockDetectorStep{},
			},
			expectNonCritical:  false,
			expectedStepsCount: 1,
		},
		{
			name: "successful init with multiple steps",
			confedSteps: []v1alpha1.DetectorSpec{
				{Name: "step1", Params: nil},
				{Name: "step2", Params: nil},
			},
			supportedSteps: map[string]Detector[mockDetection]{
				"step1": &mockDetectorStep{},
				"step2": &mockDetectorStep{},
			},
			expectNonCritical:  false,
			expectedStepsCount: 2,
		},
		{
			name: "unsupported step returns non-critical error",
			confedSteps: []v1alpha1.DetectorSpec{
				{Name: "unsupported_step", Params: nil},
			},
			supportedSteps:     map[string]Detector[mockDetection]{},
			expectNonCritical:  true,
			expectedStepsCount: 0,
		},
		{
			name: "step init error returns non-critical error",
			confedSteps: []v1alpha1.DetectorSpec{
				{Name: "failing_step", Params: nil},
			},
			supportedSteps: map[string]Detector[mockDetection]{
				"failing_step": &mockDetectorStep{initErr: errors.New("init failed")},
			},
			expectNonCritical:  true,
			expectedStepsCount: 0,
		},
		{
			name:               "empty configuration",
			confedSteps:        []v1alpha1.DetectorSpec{},
			supportedSteps:     map[string]Detector[mockDetection]{},
			expectNonCritical:  false,
			expectedStepsCount: 0,
		},
		{
			name: "mixed valid and invalid steps",
			confedSteps: []v1alpha1.DetectorSpec{
				{Name: "valid_step", Params: nil},
				{Name: "invalid_step", Params: nil},
			},
			supportedSteps: map[string]Detector[mockDetection]{
				"valid_step": &mockDetectorStep{},
			},
			expectNonCritical:  true,
			expectedStepsCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl := fake.NewClientBuilder().Build()
			pipeline := &DetectorPipeline[mockDetection]{
				Client:  cl,
				Monitor: DetectorPipelineMonitor{},
			}

			errs := pipeline.Init(
				context.Background(),
				tt.confedSteps,
				tt.supportedSteps,
			)

			if tt.expectNonCritical && len(errs) == 0 {
				t.Errorf("expected non-critical errors, got none")
			}
			if !tt.expectNonCritical && len(errs) > 0 {
				t.Errorf("did not expect non-critical errors, got: %v", errs)
			}
			if len(pipeline.steps) != tt.expectedStepsCount {
				t.Errorf("expected %d steps, got %d", tt.expectedStepsCount, len(pipeline.steps))
			}
		})
	}
}

func TestDetectorPipeline_Run(t *testing.T) {
	tests := []struct {
		name           string
		steps          map[string]Detector[mockDetection]
		expectedCount  int
		expectedSteps  []string
		stepWithErrors bool
	}{
		{
			name: "run single step successfully",
			steps: map[string]Detector[mockDetection]{
				"step1": &mockDetectorStep{
					decisions: []mockDetection{
						{resource: "vm1", host: "host1", reason: "reason1"},
					},
				},
			},
			expectedCount: 1,
			expectedSteps: []string{"step1"},
		},
		{
			name: "run multiple steps successfully",
			steps: map[string]Detector[mockDetection]{
				"step1": &mockDetectorStep{
					decisions: []mockDetection{
						{resource: "vm1", host: "host1", reason: "reason1"},
					},
				},
				"step2": &mockDetectorStep{
					decisions: []mockDetection{
						{resource: "vm2", host: "host2", reason: "reason2"},
					},
				},
			},
			expectedCount: 2,
			expectedSteps: []string{"step1", "step2"},
		},
		{
			name: "step with error is skipped",
			steps: map[string]Detector[mockDetection]{
				"failing_step": &mockDetectorStep{
					runErr: errors.New("run failed"),
				},
				"working_step": &mockDetectorStep{
					decisions: []mockDetection{
						{resource: "vm1", host: "host1", reason: "reason1"},
					},
				},
			},
			expectedCount:  1,
			expectedSteps:  []string{"working_step"},
			stepWithErrors: true,
		},
		{
			name: "step returning ErrStepSkipped is skipped",
			steps: map[string]Detector[mockDetection]{
				"skipped_step": &mockDetectorStep{
					runErr: ErrStepSkipped,
				},
				"working_step": &mockDetectorStep{
					decisions: []mockDetection{
						{resource: "vm1", host: "host1", reason: "reason1"},
					},
				},
			},
			expectedCount: 1,
			expectedSteps: []string{"working_step"},
		},
		{
			name:          "empty pipeline",
			steps:         map[string]Detector[mockDetection]{},
			expectedCount: 0,
			expectedSteps: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipeline := &DetectorPipeline[mockDetection]{
				steps:   tt.steps,
				Monitor: DetectorPipelineMonitor{},
			}

			result := pipeline.Run()

			if len(result) != tt.expectedCount {
				t.Errorf("expected %d step results, got %d", tt.expectedCount, len(result))
			}

			for _, stepName := range tt.expectedSteps {
				if _, ok := result[stepName]; !ok {
					t.Errorf("expected step %s in result", stepName)
				}
			}
		})
	}
}

func TestDetectorPipeline_Combine(t *testing.T) {
	tests := []struct {
		name            string
		decisionsByStep map[string][]mockDetection
		expectedCount   int
		expectConflict  bool
	}{
		{
			name: "combine single decision",
			decisionsByStep: map[string][]mockDetection{
				"step1": {
					{resource: "vm1", host: "host1", reason: "reason1"},
				},
			},
			expectedCount: 1,
		},
		{
			name: "combine multiple decisions from different steps",
			decisionsByStep: map[string][]mockDetection{
				"step1": {
					{resource: "vm1", host: "host1", reason: "reason1"},
				},
				"step2": {
					{resource: "vm2", host: "host2", reason: "reason2"},
				},
			},
			expectedCount: 2,
		},
		{
			name: "combine decisions for same resource with same host",
			decisionsByStep: map[string][]mockDetection{
				"step1": {
					{resource: "vm1", host: "host1", reason: "reason1"},
				},
				"step2": {
					{resource: "vm1", host: "host1", reason: "reason2"},
				},
			},
			expectedCount: 1,
		},
		{
			name: "conflicting hosts for same resource",
			decisionsByStep: map[string][]mockDetection{
				"step1": {
					{resource: "vm1", host: "host1", reason: "reason1"},
				},
				"step2": {
					{resource: "vm1", host: "host2", reason: "reason2"},
				},
			},
			expectedCount:  0,
			expectConflict: true,
		},
		{
			name:            "empty decisions",
			decisionsByStep: map[string][]mockDetection{},
			expectedCount:   0,
		},
		{
			name: "step with empty decisions",
			decisionsByStep: map[string][]mockDetection{
				"step1": {},
			},
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipeline := &DetectorPipeline[mockDetection]{}

			result := pipeline.Combine(tt.decisionsByStep)

			if len(result) != tt.expectedCount {
				t.Errorf("expected %d combined decisions, got %d", tt.expectedCount, len(result))
			}
		})
	}
}

func TestDetectorPipeline_Combine_MergedReason(t *testing.T) {
	pipeline := &DetectorPipeline[mockDetection]{}

	decisionsByStep := map[string][]mockDetection{
		"step1": {
			{resource: "vm1", host: "host1", reason: "reason1"},
		},
		"step2": {
			{resource: "vm1", host: "host1", reason: "reason2"},
		},
	}

	result := pipeline.Combine(decisionsByStep)

	if len(result) != 1 {
		t.Fatalf("expected 1 combined decision, got %d", len(result))
	}

	// The merged reason should contain both original reasons
	reason := result[0].GetReason()
	if reason == "" {
		t.Error("expected non-empty reason")
	}
	if reason != "multiple reasons: reason1; reason2" && reason != "multiple reasons: reason2; reason1" {
		// The order might vary due to map iteration order
		if !strings.Contains(reason, "reason1") || !strings.Contains(reason, "reason2") {
			t.Errorf("expected reason to contain both 'reason1' and 'reason2', got %s", reason)
		}
	}
}

func TestDetectorPipeline_RunWithMonitor(t *testing.T) {
	// Test that Run works with a proper monitor
	monitor := NewDetectorPipelineMonitor()
	pipeline := &DetectorPipeline[mockDetection]{
		steps: map[string]Detector[mockDetection]{
			"step1": &mockDetectorStep{
				decisions: []mockDetection{
					{resource: "vm1", host: "host1", reason: "reason1"},
				},
			},
		},
		Monitor: monitor,
	}

	result := pipeline.Run()

	if len(result) != 1 {
		t.Errorf("expected 1 step result, got %d", len(result))
	}
}
