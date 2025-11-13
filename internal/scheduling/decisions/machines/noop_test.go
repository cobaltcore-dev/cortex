// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package machines

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/delegation/ironcore"
	ironcorev1alpha1 "github.com/cobaltcore-dev/cortex/api/delegation/ironcore/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNoopFilter_Run(t *testing.T) {
	tests := []struct {
		name     string
		request  ironcore.MachinePipelineRequest
		expected map[string]float64
	}{
		{
			name: "empty machine pools",
			request: ironcore.MachinePipelineRequest{
				Pools: []ironcorev1alpha1.MachinePool{},
			},
			expected: map[string]float64{},
		},
		{
			name: "single machine pool",
			request: ironcore.MachinePipelineRequest{
				Pools: []ironcorev1alpha1.MachinePool{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "pool1"},
					},
				},
			},
			expected: map[string]float64{
				"pool1": 1.0,
			},
		},
		{
			name: "multiple machine pools",
			request: ironcore.MachinePipelineRequest{
				Pools: []ironcorev1alpha1.MachinePool{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "pool1"},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "pool2"},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "pool3"},
					},
				},
			},
			expected: map[string]float64{
				"pool1": 1.0,
				"pool2": 1.0,
				"pool3": 1.0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := &NoopFilter{}
			result, err := filter.Run(slog.Default(), tt.request)

			if err != nil {
				t.Errorf("expected Run() to succeed, got error: %v", err)
				return
			}

			if result == nil {
				t.Fatal("expected result to be non-nil")
			}

			if len(result.Activations) != len(tt.expected) {
				t.Errorf("expected %d activations, got %d", len(tt.expected), len(result.Activations))
				return
			}

			for poolName, expectedWeight := range tt.expected {
				actualWeight, ok := result.Activations[poolName]
				if !ok {
					t.Errorf("expected activation for pool %q, but not found", poolName)
					continue
				}

				if actualWeight != expectedWeight {
					t.Errorf("expected weight for pool %q to be %f, got %f", poolName, expectedWeight, actualWeight)
				}
			}

			if result.Statistics == nil {
				t.Error("expected Statistics to be non-nil")
			}
		})
	}
}
