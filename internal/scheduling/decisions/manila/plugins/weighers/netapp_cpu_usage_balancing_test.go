// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/delegation/manila"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/netapp"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNetappCPUUsageBalancingStep_Run(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	storagePoolCPUUsage, err := v1alpha1.BoxFeatureList([]any{
		&netapp.StoragePoolCPUUsage{StoragePoolName: "pool1", AvgCPUUsagePct: 0.0, MaxCPUUsagePct: 0.0},
		&netapp.StoragePoolCPUUsage{StoragePoolName: "pool2", AvgCPUUsagePct: 100.0, MaxCPUUsagePct: 0.0},
		&netapp.StoragePoolCPUUsage{StoragePoolName: "pool3", AvgCPUUsagePct: 0.0, MaxCPUUsagePct: 100.0},
		&netapp.StoragePoolCPUUsage{StoragePoolName: "pool4", AvgCPUUsagePct: 100.0, MaxCPUUsagePct: 100.0},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Create an instance of the step
	step := &NetappCPUUsageBalancingStep{}
	step.Options.AvgCPUUsageLowerBound = 0.0
	step.Options.AvgCPUUsageUpperBound = 100.0
	step.Options.AvgCPUUsageActivationLowerBound = 0.0
	step.Options.AvgCPUUsageActivationUpperBound = -1.0
	step.Options.MaxCPUUsageLowerBound = 0.0
	step.Options.MaxCPUUsageUpperBound = 100.0
	step.Options.MaxCPUUsageActivationLowerBound = 0.0
	step.Options.MaxCPUUsageActivationUpperBound = -1.0
	step.Client = fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&v1alpha1.Knowledge{
			ObjectMeta: v1.ObjectMeta{Name: "netapp-storage-pool-cpu-usage-manila"},
			Status:     v1alpha1.KnowledgeStatus{Raw: storagePoolCPUUsage},
		}).
		Build()

	tests := []struct {
		name     string
		request  api.ExternalSchedulerRequest
		expected map[string]float64
	}{
		{
			name: "Avoid contended pools",
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ShareHost: "pool1"},
					{ShareHost: "pool2"},
					{ShareHost: "pool3"},
					{ShareHost: "pool4"},
				},
			},
			expected: map[string]float64{
				"pool1": 0,
				"pool2": -1,
				"pool3": -1,
				"pool4": -2, // Max and avg usage stack up.
			},
		},
		{
			name: "Missing data",
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ShareHost: "pool4"},
					{ShareHost: "pool5"}, // No data for pool5
				},
			},
			expected: map[string]float64{
				"pool4": -2,
				"pool5": 0, // No data but still contained in the result.
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := step.Run(slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			// Check that the weights have decreased
			for pool, weight := range result.Activations {
				expected := tt.expected[pool]
				if weight != expected {
					t.Errorf("expected weight for pool %s to be %f, got %f", pool, expected, weight)
				}
			}
		})
	}
}
