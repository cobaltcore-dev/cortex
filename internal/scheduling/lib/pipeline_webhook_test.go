// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"errors"
	"strings"
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
		name        string
		pipeline    *v1alpha1.Pipeline
		filters     map[string]Validatable
		weighers    map[string]Validatable
		detectors   map[string]Validatable
		expectError bool
		errorMsg    string
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
			detectors:   map[string]Validatable{},
			expectError: false,
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
			filters:     map[string]Validatable{},
			weighers:    map[string]Validatable{},
			detectors:   map[string]Validatable{},
			expectError: true,
			errorMsg:    "unknown filter",
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
			filters:     map[string]Validatable{},
			weighers:    map[string]Validatable{},
			detectors:   map[string]Validatable{},
			expectError: true,
			errorMsg:    "unknown weigher",
		},
		{
			name: "invalid filter-weigher pipeline with detectors",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pipeline"},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					Detectors: []v1alpha1.DetectorSpec{
						{Name: "detector1", Params: nil},
					},
				},
			},
			filters:     map[string]Validatable{},
			weighers:    map[string]Validatable{},
			detectors:   map[string]Validatable{},
			expectError: true,
			errorMsg:    "detectors are not allowed in a filter/weigher pipeline",
		},
		{
			name: "filter validation error",
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
			weighers:    map[string]Validatable{},
			detectors:   map[string]Validatable{},
			expectError: true,
			errorMsg:    "filter validation failed",
		},
		{
			name: "weigher validation error",
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
			detectors:   map[string]Validatable{},
			expectError: true,
			errorMsg:    "weigher validation failed",
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

			_, err := webhook.ValidateCreate(t.Context(), tt.pipeline)

			if tt.expectError && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}
			if tt.expectError && err != nil && tt.errorMsg != "" {
				if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error message to contain %q, got %q", tt.errorMsg, err.Error())
				}
			}
		})
	}
}

func TestPipelineAdmissionWebhook_ValidateCreate_DetectorPipeline(t *testing.T) {
	tests := []struct {
		name        string
		pipeline    *v1alpha1.Pipeline
		filters     map[string]Validatable
		weighers    map[string]Validatable
		detectors   map[string]Validatable
		expectError bool
		errorMsg    string
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
			expectError: false,
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
			filters:     map[string]Validatable{},
			weighers:    map[string]Validatable{},
			detectors:   map[string]Validatable{},
			expectError: true,
			errorMsg:    "unknown detector",
		},
		{
			name: "invalid detector pipeline with filters",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pipeline"},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeDetector,
					Filters: []v1alpha1.FilterSpec{
						{Name: "filter1", Params: nil},
					},
				},
			},
			filters:     map[string]Validatable{},
			weighers:    map[string]Validatable{},
			detectors:   map[string]Validatable{},
			expectError: true,
			errorMsg:    "filters are not allowed in a detector pipeline",
		},
		{
			name: "invalid detector pipeline with weighers",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pipeline"},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeDetector,
					Weighers: []v1alpha1.WeigherSpec{
						{Name: "weigher1", Params: nil},
					},
				},
			},
			filters:     map[string]Validatable{},
			weighers:    map[string]Validatable{},
			detectors:   map[string]Validatable{},
			expectError: true,
			errorMsg:    "weighers are not allowed in a detector pipeline",
		},
		{
			name: "detector validation error",
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
			expectError: true,
			errorMsg:    "detector validation failed",
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

			_, err := webhook.ValidateCreate(t.Context(), tt.pipeline)

			if tt.expectError && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}
			if tt.expectError && err != nil && tt.errorMsg != "" {
				if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error message to contain %q, got %q", tt.errorMsg, err.Error())
				}
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

	if err == nil {
		t.Error("expected error for unknown pipeline type, got nil")
	}
	if !strings.Contains(err.Error(), "unknown pipeline type") {
		t.Errorf("expected error message to contain 'unknown pipeline type', got %q", err.Error())
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

func TestPipelineAdmissionWebhook_MultipleValidationErrors(t *testing.T) {
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

	_, err := webhook.ValidateCreate(t.Context(), pipeline)

	if err == nil {
		t.Error("expected error for unknown filters and weighers, got nil")
	}

	// Check that multiple errors are reported
	errStr := err.Error()
	if !strings.Contains(errStr, "unknown-filter1") {
		t.Error("expected error to contain 'unknown-filter1'")
	}
	if !strings.Contains(errStr, "unknown-filter2") {
		t.Error("expected error to contain 'unknown-filter2'")
	}
	if !strings.Contains(errStr, "unknown-weigher1") {
		t.Error("expected error to contain 'unknown-weigher1'")
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
