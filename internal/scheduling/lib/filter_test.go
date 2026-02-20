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

type mockFilter[RequestType FilterWeigherPipelineRequest] struct {
	InitFunc     func(ctx context.Context, client client.Client, step v1alpha1.FilterSpec) error
	ValidateFunc func(ctx context.Context, params runtime.RawExtension) error
	RunFunc      func(traceLog *slog.Logger, request RequestType) (*FilterWeigherPipelineStepResult, error)
}

func (m *mockFilter[RequestType]) Init(ctx context.Context, client client.Client, step v1alpha1.FilterSpec) error {
	if m.InitFunc == nil {
		return nil
	}
	return m.InitFunc(ctx, client, step)
}
func (m *mockFilter[RequestType]) Validate(ctx context.Context, params runtime.RawExtension) error {
	if m.ValidateFunc == nil {
		return nil
	}
	return m.ValidateFunc(ctx, params)
}
func (m *mockFilter[RequestType]) Run(traceLog *slog.Logger, request RequestType) (*FilterWeigherPipelineStepResult, error) {
	if m.RunFunc == nil {
		return &FilterWeigherPipelineStepResult{}, nil
	}
	return m.RunFunc(traceLog, request)
}

// filterTestOptions implements FilterWeigherPipelineStepOpts for testing.
type filterTestOptions struct{}

func (o filterTestOptions) Validate() error { return nil }

func TestBaseFilter_Init(t *testing.T) {
	tests := []struct {
		name        string
		filterSpec  v1alpha1.FilterSpec
		expectError bool
	}{
		{
			name: "successful initialization with valid params",
			filterSpec: v1alpha1.FilterSpec{
				Name: "test-filter",
				Params: runtime.RawExtension{
					Raw: []byte(`{}`),
				},
			},
			expectError: false,
		},
		{
			name: "successful initialization with empty params",
			filterSpec: v1alpha1.FilterSpec{
				Name: "test-filter",
				Params: runtime.RawExtension{
					Raw: []byte(`{}`),
				},
			},
			expectError: false,
		},
		{
			name: "error on invalid JSON params",
			filterSpec: v1alpha1.FilterSpec{
				Name: "test-filter",
				Params: runtime.RawExtension{
					Raw: []byte(`{invalid json}`),
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := &BaseFilter[mockFilterWeigherPipelineRequest, filterTestOptions]{}
			cl := fake.NewClientBuilder().Build()

			err := filter.Init(t.Context(), cl, tt.filterSpec)

			if tt.expectError && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}
			if !tt.expectError && filter.Client == nil {
				t.Error("expected client to be set but it was nil")
			}
		})
	}
}
