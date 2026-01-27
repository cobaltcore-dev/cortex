// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type mockWeigher[RequestType PipelineRequest] struct {
	InitFunc func(ctx context.Context, client client.Client, step v1alpha1.WeigherSpec) error
	RunFunc  func(traceLog *slog.Logger, request RequestType) (*StepResult, error)
}

func (m *mockWeigher[RequestType]) Init(ctx context.Context, client client.Client, step v1alpha1.WeigherSpec) error {
	if m.InitFunc == nil {
		return nil
	}
	return m.InitFunc(ctx, client, step)
}
func (m *mockWeigher[RequestType]) Run(traceLog *slog.Logger, request RequestType) (*StepResult, error) {
	if m.RunFunc == nil {
		return &StepResult{}, nil
	}
	return m.RunFunc(traceLog, request)
}
