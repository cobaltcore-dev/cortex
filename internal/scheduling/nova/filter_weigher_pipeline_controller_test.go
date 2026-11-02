// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

// mockCandidateGatherer implements CandidateGatherer for testing
type mockCandidateGatherer struct {
	called        bool
	err           error
	gatheredHosts []compute.Host
}

func (m *mockCandidateGatherer) MutateWithAllCandidates(ctx context.Context, request *compute.SchedulerRequest) error {
	m.called = true
	if m.err != nil {
		return m.err
	}
	if m.gatheredHosts != nil {
		request.Hosts = m.gatheredHosts
		request.Weights = make(map[string]float64)
		for _, host := range m.gatheredHosts {
			request.Weights[host.ComputeHost] = 0.0
		}
	}
	return nil
}

func TestFilterWeigherPipelineController_InitPipeline(t *testing.T) {
	controller := &FilterWeigherPipelineController{
		Monitor: lib.FilterWeigherPipelineMonitor{},
	}

	tests := []struct {
		name                   string
		filters                []v1alpha1.FilterSpec
		weighers               []v1alpha1.WeigherSpec
		expectNonCriticalError bool
		expectCriticalError    bool
		expectUnknownFilter    bool
		expectUnknownWeigher   bool
	}{
		{
			name:                   "empty steps",
			filters:                []v1alpha1.FilterSpec{},
			weighers:               []v1alpha1.WeigherSpec{},
			expectNonCriticalError: false,
			expectCriticalError:    false,
			expectUnknownFilter:    false,
			expectUnknownWeigher:   false,
		},
		{
			name: "supported step",
			filters: []v1alpha1.FilterSpec{
				{
					Name: "filter_status_conditions",
				},
			},
			expectNonCriticalError: false,
			expectCriticalError:    false,
			expectUnknownFilter:    false,
			expectUnknownWeigher:   false,
		},
		{
			name: "unsupported step",
			filters: []v1alpha1.FilterSpec{
				{
					Name: "unsupported-plugin",
				},
			},
			expectNonCriticalError: false,
			expectCriticalError:    false,
			expectUnknownFilter:    true,
			expectUnknownWeigher:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initResult := controller.InitPipeline(t.Context(), v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Filters:  tt.filters,
					Weighers: tt.weighers,
				},
			})

			if tt.expectCriticalError && len(initResult.FilterErrors) == 0 {
				t.Error("Expected critical error but got none")
			}
			if !tt.expectCriticalError && len(initResult.FilterErrors) > 0 {
				t.Errorf("Unexpected critical errors: %v", initResult.FilterErrors)
			}
			if tt.expectNonCriticalError && len(initResult.WeigherErrors) == 0 {
				t.Error("Expected non-critical error but got none")
			}
			if !tt.expectNonCriticalError && len(initResult.WeigherErrors) > 0 {
				t.Errorf("Unexpected non-critical errors: %v", initResult.WeigherErrors)
			}
		})
	}
}
