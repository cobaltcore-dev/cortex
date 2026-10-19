// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"errors"
	"fmt"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

func ptr[T any](v T) *T { return &v }

func TestGenerateExplanation(t *testing.T) {
	tests := []struct {
		name     string
		result   *v1alpha1.DecisionResult
		err      error
		expected string
	}{
		{
			name:     "nil result no error",
			result:   nil,
			err:      nil,
			expected: "No decision result recorded.",
		},
		{
			name:     "nil result with error",
			result:   nil,
			err:      errors.New("something broke"),
			expected: "Pipeline run failed: something broke.",
		},
		{
			name: "result with target host only no steps",
			result: &v1alpha1.DecisionResult{
				TargetHost: ptr("host-1"),
			},
			expected: "Selected host: host-1.",
		},
		{
			name: "result with RawInWeights and filtering steps",
			result: &v1alpha1.DecisionResult{
				RawInWeights: map[string]float64{
					"host-a": 1.0,
					"host-b": 0.5,
					"host-c": 0.3,
				},
				StepResults: []v1alpha1.StepResult{
					{
						StepName: "filter_capacity",
						Activations: map[string]float64{
							"host-a": 1.0,
							"host-b": 0.5,
						},
					},
					{
						StepName: "filter_status",
						Activations: map[string]float64{
							"host-a": 1.0,
						},
					},
				},
				TargetHost: ptr("host-a"),
			},
			expected: "Started with 3 host(s).\n\n" +
				"filter_capacity filtered out host-c\n" +
				"filter_status filtered out host-b\n\n" +
				"1 hosts remaining (host-a)\n\n" +
				"Selected host: host-a.",
		},
		{
			name: "uses NormalizedInWeights when RawInWeights empty",
			result: &v1alpha1.DecisionResult{
				NormalizedInWeights: map[string]float64{
					"host-x": 0.6,
					"host-y": 0.4,
				},
				StepResults: []v1alpha1.StepResult{
					{
						StepName: "weigher_cpu",
						Activations: map[string]float64{
							"host-x": 0.8,
							"host-y": 0.2,
						},
					},
				},
				TargetHost: ptr("host-x"),
			},
			expected: "Started with 2 host(s).\n\n\n2 hosts remaining (host-x, host-y)\n\nSelected host: host-x.",
		},
		{
			name: "no weights no target",
			result: &v1alpha1.DecisionResult{
				StepResults: []v1alpha1.StepResult{
					{StepName: "some-step", Activations: map[string]float64{}},
				},
			},
			expected: "No host weight data recorded.",
		},
		{
			name: "no weights with target",
			result: &v1alpha1.DecisionResult{
				TargetHost: ptr("host-1"),
				StepResults: []v1alpha1.StepResult{
					{StepName: "some-step", Activations: map[string]float64{}},
				},
			},
			expected: "Selected host: host-1.",
		},
		{
			name: "all hosts survive all steps",
			result: &v1alpha1.DecisionResult{
				RawInWeights: map[string]float64{
					"host-a": 1.0,
					"host-b": 0.5,
				},
				StepResults: []v1alpha1.StepResult{
					{
						StepName: "noop",
						Activations: map[string]float64{
							"host-a": 1.0,
							"host-b": 0.5,
						},
					},
				},
				TargetHost: ptr("host-a"),
			},
			expected: "Started with 2 host(s).\n\n\n2 hosts remaining (host-a, host-b)\n\nSelected host: host-a.",
		},
		{
			name: "large cluster caps host lists",
			result: func() *v1alpha1.DecisionResult {
				weights := make(map[string]float64, 50)
				for i := range 50 {
					weights[fmt.Sprintf("host-%03d", i)] = 1.0
				}
				// Step filters out 20 hosts (keep host-000..host-029).
				surviving := make(map[string]float64, 30)
				for i := range 30 {
					surviving[fmt.Sprintf("host-%03d", i)] = 1.0
				}
				return &v1alpha1.DecisionResult{
					RawInWeights: weights,
					StepResults: []v1alpha1.StepResult{
						{
							StepName:    "filter_big",
							Activations: surviving,
						},
					},
					TargetHost: ptr("host-000"),
				}
			}(),
			expected: "Started with 50 host(s).\n\n" +
				"filter_big filtered out host-030, host-031, host-032, host-033, host-034, host-035, host-036, host-037, host-038, host-039 (and 10 more)\n\n" +
				"30 hosts remaining (host-000, host-001, host-002, host-003, host-004, host-005, host-006, host-007, host-008, host-009 (and 20 more))\n\n" +
				"Selected host: host-000.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateExplanation(tt.result, tt.err)
			if got != tt.expected {
				t.Errorf("GenerateExplanation() =\n%q\nwant:\n%q", got, tt.expected)
			}
		})
	}
}
