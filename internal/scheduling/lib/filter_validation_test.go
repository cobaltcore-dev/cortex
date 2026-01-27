// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestValidateFilter(t *testing.T) {
	filter := &mockFilter[mockFilterWeigherPipelineRequest]{}
	validator := validateFilter(filter)

	if validator == nil {
		t.Fatal("expected validator but got nil")
	}
	if validator.Filter != filter {
		t.Error("expected filter to be set in validator")
	}
}

func TestFilterValidator_Init(t *testing.T) {
	tests := []struct {
		name        string
		filterSpec  v1alpha1.FilterSpec
		initError   error
		expectError bool
	}{
		{
			name: "successful initialization",
			filterSpec: v1alpha1.FilterSpec{
				Name: "test-filter",
				Params: runtime.RawExtension{
					Raw: []byte(`{}`),
				},
			},
			initError:   nil,
			expectError: false,
		},
		{
			name: "initialization error",
			filterSpec: v1alpha1.FilterSpec{
				Name: "test-filter",
				Params: runtime.RawExtension{
					Raw: []byte(`{}`),
				},
			},
			initError:   errors.New("init error"),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := &mockFilter[mockFilterWeigherPipelineRequest]{
				InitFunc: func(_ context.Context, _ client.Client, _ v1alpha1.FilterSpec) error {
					return tt.initError
				},
			}
			validator := validateFilter(filter)
			cl := fake.NewClientBuilder().Build()

			err := validator.Init(t.Context(), cl, tt.filterSpec)

			if tt.expectError && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}
		})
	}
}

func TestFilterValidator_Run(t *testing.T) {
	tests := []struct {
		name          string
		subjects      []string
		runResult     *FilterWeigherPipelineStepResult
		runError      error
		expectError   bool
		errorContains string
	}{
		{
			name:     "successful run - filter removes some subjects",
			subjects: []string{"host1", "host2", "host3"},
			runResult: &FilterWeigherPipelineStepResult{
				Activations: map[string]float64{
					"host1": 1.0,
					"host2": 1.0,
				},
			},
			runError:    nil,
			expectError: false,
		},
		{
			name:     "successful run - filter keeps all subjects",
			subjects: []string{"host1", "host2"},
			runResult: &FilterWeigherPipelineStepResult{
				Activations: map[string]float64{
					"host1": 1.0,
					"host2": 1.0,
				},
			},
			runError:    nil,
			expectError: false,
		},
		{
			name:        "run error from filter",
			subjects:    []string{"host1"},
			runResult:   nil,
			runError:    errors.New("filter error"),
			expectError: true,
		},
		{
			name:     "validation error - subjects increased",
			subjects: []string{"host1"},
			runResult: &FilterWeigherPipelineStepResult{
				Activations: map[string]float64{
					"host1": 1.0,
					"host2": 1.0,
					"host3": 1.0,
				},
			},
			runError:      nil,
			expectError:   true,
			errorContains: "number of subjects increased",
		},
		{
			name:     "handle duplicate subjects in request",
			subjects: []string{"host1", "host1", "host2"},
			runResult: &FilterWeigherPipelineStepResult{
				Activations: map[string]float64{
					"host1": 1.0,
					"host2": 1.0,
				},
			},
			runError:    nil,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := &mockFilter[mockFilterWeigherPipelineRequest]{
				RunFunc: func(traceLog *slog.Logger, request mockFilterWeigherPipelineRequest) (*FilterWeigherPipelineStepResult, error) {
					return tt.runResult, tt.runError
				},
			}
			validator := validateFilter(filter)
			request := mockFilterWeigherPipelineRequest{
				Subjects: tt.subjects,
			}
			traceLog := slog.Default()

			result, err := validator.Run(traceLog, request)

			if tt.expectError && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}
			if tt.expectError && tt.errorContains != "" && err != nil {
				if !containsStr(err.Error(), tt.errorContains) {
					t.Errorf("expected error to contain %q, got %q", tt.errorContains, err.Error())
				}
			}
			if !tt.expectError && result == nil {
				t.Error("expected result but got nil")
			}
		})
	}
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
