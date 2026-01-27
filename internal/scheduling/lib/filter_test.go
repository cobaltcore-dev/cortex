// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type mockFilter[RequestType PipelineRequest] struct {
	InitFunc func(ctx context.Context, client client.Client, step v1alpha1.FilterSpec) error
	RunFunc  func(traceLog *slog.Logger, request RequestType) (*StepResult, error)
}

func (m *mockFilter[RequestType]) Init(ctx context.Context, client client.Client, step v1alpha1.FilterSpec) error {
	if m.InitFunc == nil {
		return nil
	}
	return m.InitFunc(ctx, client, step)
}
func (m *mockFilter[RequestType]) Run(traceLog *slog.Logger, request RequestType) (*StepResult, error) {
	if m.RunFunc == nil {
		return &StepResult{}, nil
	}
	return m.RunFunc(traceLog, request)
}
