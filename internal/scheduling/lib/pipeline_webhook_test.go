// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"errors"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// mockValidatable implements Validatable for testing.
type mockValidatable struct {
	ValidateFunc func(ctx context.Context, params v1alpha1.Parameters) error
}

func (m *mockValidatable) Validate(ctx context.Context, params v1alpha1.Parameters) error {
	if m.ValidateFunc == nil {
		return nil
	}
	return m.ValidateFunc(ctx, params)
}

func TestPipelineAdmissionWebhook_ValidateCreate_FilterWeigherPipeline(t *testing.T) {
	tests := []struct {
		name           string
		pipeline       *v1alpha1.Pipeline
		filters        map[string]Validatable
		weighers       map[string]Validatable
		detectors      map[string]Validatable
		expectError    bool
		expectWarnings bool
	}{
		{
			name: "valid filter-weigher pipeline with known filter and weigher",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pipeline"},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					Filters: []v1alpha1.FilterSpec{
						{Name: "filter1", Params: nil},
					},
					Weighers: []v1alpha1.WeigherSpec{
						{Name: "weigher1", Params: nil},
					},
				},
			},
			filters: map[string]Validatable{
				"filter1": &mockValidatable{},
			},
			weighers: map[string]Validatable{
				"weigher1": &mockValidatable{},
			},
			detectors:      map[string]Validatable{},
			expectError:    false,
			expectWarnings: false,
		},
		{
			name: "invalid filter-weigher pipeline with unknown filter",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pipeline"},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					Filters: []v1alpha1.FilterSpec{
						{Name: "unknown-filter", Params: nil},
					},
				},
			},
			filters:        map[string]Validatable{},
			weighers:       map[string]Validatable{},
			detectors:      map[string]Validatable{},
			expectError:    false,
			expectWarnings: true,
		},
		{
			name: "invalid filter-weigher pipeline with unknown weigher",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pipeline"},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					Weighers: []v1alpha1.WeigherSpec{
						{Name: "unknown-weigher", Params: nil},
					},
				},
			},
			filters:        map[string]Validatable{},
			weighers:       map[string]Validatable{},
			detectors:      map[string]Validatable{},
			expectError:    false,
			expectWarnings: true,
		},
		{
			name: "filter validation failure surfaces as warning",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pipeline"},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					Filters: []v1alpha1.FilterSpec{
						{Name: "filter1", Params: nil},
					},
				},
			},
			filters: map[string]Validatable{
				"filter1": &mockValidatable{
					ValidateFunc: func(ctx context.Context, params v1alpha1.Parameters) error {
						return errors.New("filter validation failed")
					},
				},
			},
			weighers:       map[string]Validatable{},
			detectors:      map[string]Validatable{},
			expectError:    false,
			expectWarnings: true,
		},
		{
			name: "weigher validation failure surfaces as warning",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pipeline"},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					Weighers: []v1alpha1.WeigherSpec{
						{Name: "weigher1", Params: nil},
					},
				},
			},
			filters: map[string]Validatable{},
			weighers: map[string]Validatable{
				"weigher1": &mockValidatable{
					ValidateFunc: func(ctx context.Context, params v1alpha1.Parameters) error {
						return errors.New("weigher validation failed")
					},
				},
			},
			detectors:      map[string]Validatable{},
			expectError:    false,
			expectWarnings: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			webhook := &PipelineAdmissionWebhook{
				SchedulingDomain:     v1alpha1.SchedulingDomainNova,
				ValidatableFilters:   tt.filters,
				ValidatableWeighers:  tt.weighers,
				ValidatableDetectors: tt.detectors,
			}

			warnings, err := webhook.ValidateCreate(t.Context(), tt.pipeline)

			if tt.expectError && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}
			if tt.expectWarnings && len(warnings) == 0 {
				t.Error("expected warnings but got none")
			}
			if !tt.expectWarnings && len(warnings) > 0 {
				t.Errorf("expected no warnings but got: %v", warnings)
			}
		})
	}
}

func TestPipelineAdmissionWebhook_ValidateCreate_DetectorPipeline(t *testing.T) {
	tests := []struct {
		name           string
		pipeline       *v1alpha1.Pipeline
		filters        map[string]Validatable
		weighers       map[string]Validatable
		detectors      map[string]Validatable
		expectError    bool
		expectWarnings bool
	}{
		{
			name: "valid detector pipeline with known detector",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pipeline"},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeDetector,
					Detectors: []v1alpha1.DetectorSpec{
						{Name: "detector1", Params: nil},
					},
				},
			},
			filters:  map[string]Validatable{},
			weighers: map[string]Validatable{},
			detectors: map[string]Validatable{
				"detector1": &mockValidatable{},
			},
			expectError:    false,
			expectWarnings: false,
		},
		{
			name: "invalid detector pipeline with unknown detector",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pipeline"},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeDetector,
					Detectors: []v1alpha1.DetectorSpec{
						{Name: "unknown-detector", Params: nil},
					},
				},
			},
			filters:        map[string]Validatable{},
			weighers:       map[string]Validatable{},
			detectors:      map[string]Validatable{},
			expectError:    false,
			expectWarnings: true,
		},
		{
			name: "detector validation failure surfaces as warning",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pipeline"},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeDetector,
					Detectors: []v1alpha1.DetectorSpec{
						{Name: "detector1", Params: nil},
					},
				},
			},
			filters:  map[string]Validatable{},
			weighers: map[string]Validatable{},
			detectors: map[string]Validatable{
				"detector1": &mockValidatable{
					ValidateFunc: func(ctx context.Context, params v1alpha1.Parameters) error {
						return errors.New("detector validation failed")
					},
				},
			},
			expectError:    false,
			expectWarnings: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			webhook := &PipelineAdmissionWebhook{
				SchedulingDomain:     v1alpha1.SchedulingDomainNova,
				ValidatableFilters:   tt.filters,
				ValidatableWeighers:  tt.weighers,
				ValidatableDetectors: tt.detectors,
			}

			warnings, err := webhook.ValidateCreate(t.Context(), tt.pipeline)

			if tt.expectError && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}
			if tt.expectWarnings && len(warnings) == 0 {
				t.Error("expected warnings but got none")
			}
			if !tt.expectWarnings && len(warnings) > 0 {
				t.Errorf("expected no warnings but got: %v", warnings)
			}
		})
	}
}

func TestPipelineAdmissionWebhook_ValidateCreate_DifferentSchedulingDomain(t *testing.T) {
	webhook := &PipelineAdmissionWebhook{
		SchedulingDomain:     v1alpha1.SchedulingDomainNova,
		ValidatableFilters:   map[string]Validatable{},
		ValidatableWeighers:  map[string]Validatable{},
		ValidatableDetectors: map[string]Validatable{},
	}

	// Pipeline for a different scheduling domain should be skipped (no validation error)
	pipeline := &v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pipeline"},
		Spec: v1alpha1.PipelineSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainCinder, // Different domain
			Type:             v1alpha1.PipelineTypeFilterWeigher,
			Filters: []v1alpha1.FilterSpec{
				{Name: "unknown-filter", Params: nil},
			},
		},
	}

	_, err := webhook.ValidateCreate(t.Context(), pipeline)

	if err != nil {
		t.Errorf("expected no error for different scheduling domain, got: %v", err)
	}
}

func TestPipelineAdmissionWebhook_ValidateCreate_UnknownPipelineType(t *testing.T) {
	// The pipeline type enum and the type/step-list constraints are enforced by
	// the API server via CEL on the CRD, not by the webhook. The webhook only
	// validates referenced steps and never rejects based on the type itself.
	webhook := &PipelineAdmissionWebhook{
		SchedulingDomain:     v1alpha1.SchedulingDomainNova,
		ValidatableFilters:   map[string]Validatable{},
		ValidatableWeighers:  map[string]Validatable{},
		ValidatableDetectors: map[string]Validatable{},
	}

	pipeline := &v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pipeline"},
		Spec: v1alpha1.PipelineSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Type:             "unknown-type",
		},
	}

	_, err := webhook.ValidateCreate(t.Context(), pipeline)

	if err != nil {
		t.Errorf("expected no error from the webhook for pipeline type, got: %v", err)
	}
}

func TestPipelineAdmissionWebhook_ValidateUpdate(t *testing.T) {
	webhook := &PipelineAdmissionWebhook{
		SchedulingDomain: v1alpha1.SchedulingDomainNova,
		ValidatableFilters: map[string]Validatable{
			"filter1": &mockValidatable{},
		},
		ValidatableWeighers:  map[string]Validatable{},
		ValidatableDetectors: map[string]Validatable{},
	}

	oldPipeline := &v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pipeline"},
		Spec: v1alpha1.PipelineSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Type:             v1alpha1.PipelineTypeFilterWeigher,
		},
	}

	newPipeline := &v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pipeline"},
		Spec: v1alpha1.PipelineSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Type:             v1alpha1.PipelineTypeFilterWeigher,
			Filters: []v1alpha1.FilterSpec{
				{Name: "filter1", Params: nil},
			},
		},
	}

	_, err := webhook.ValidateUpdate(t.Context(), oldPipeline, newPipeline)

	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestPipelineAdmissionWebhook_ValidateDelete(t *testing.T) {
	webhook := &PipelineAdmissionWebhook{
		SchedulingDomain:     v1alpha1.SchedulingDomainNova,
		ValidatableFilters:   map[string]Validatable{},
		ValidatableWeighers:  map[string]Validatable{},
		ValidatableDetectors: map[string]Validatable{},
	}

	pipeline := &v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pipeline"},
		Spec: v1alpha1.PipelineSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Type:             v1alpha1.PipelineTypeFilterWeigher,
		},
	}

	_, err := webhook.ValidateDelete(t.Context(), pipeline)

	if err != nil {
		t.Errorf("expected no error for delete, got: %v", err)
	}
}

func TestPipelineAdmissionWebhook_MultipleValidationWarnings(t *testing.T) {
	webhook := &PipelineAdmissionWebhook{
		SchedulingDomain:     v1alpha1.SchedulingDomainNova,
		ValidatableFilters:   map[string]Validatable{},
		ValidatableWeighers:  map[string]Validatable{},
		ValidatableDetectors: map[string]Validatable{},
	}

	// Pipeline with multiple unknown filters and weighers
	pipeline := &v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pipeline"},
		Spec: v1alpha1.PipelineSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Type:             v1alpha1.PipelineTypeFilterWeigher,
			Filters: []v1alpha1.FilterSpec{
				{Name: "unknown-filter1", Params: nil},
				{Name: "unknown-filter2", Params: nil},
			},
			Weighers: []v1alpha1.WeigherSpec{
				{Name: "unknown-weigher1", Params: nil},
			},
		},
	}

	warnings, err := webhook.ValidateCreate(t.Context(), pipeline)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
	if len(warnings) != 3 {
		t.Errorf("expected 3 warnings for unknown filters and weighers, got %d: %v", len(warnings), warnings)
	}
}

func TestPipelineAdmissionWebhook_EmptyPipeline(t *testing.T) {
	webhook := &PipelineAdmissionWebhook{
		SchedulingDomain:     v1alpha1.SchedulingDomainNova,
		ValidatableFilters:   map[string]Validatable{},
		ValidatableWeighers:  map[string]Validatable{},
		ValidatableDetectors: map[string]Validatable{},
	}

	// Empty filter-weigher pipeline (no filters, no weighers) should be valid
	pipeline := &v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pipeline"},
		Spec: v1alpha1.PipelineSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Type:             v1alpha1.PipelineTypeFilterWeigher,
		},
	}

	_, err := webhook.ValidateCreate(t.Context(), pipeline)

	if err != nil {
		t.Errorf("expected no error for empty pipeline, got: %v", err)
	}
}
