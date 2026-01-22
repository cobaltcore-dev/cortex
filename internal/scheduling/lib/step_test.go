// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type mockStep[RequestType PipelineRequest, StepType v1alpha1.Step] struct {
	InitFunc func(ctx context.Context, client client.Client, step StepType) error
	RunFunc  func(traceLog *slog.Logger, request RequestType) (*StepResult, error)
}

func (m *mockStep[RequestType, StepType]) Init(ctx context.Context, client client.Client, step StepType) error {
	return m.InitFunc(ctx, client, step)
}
func (m *mockStep[RequestType, StepType]) Run(traceLog *slog.Logger, request RequestType) (*StepResult, error) {
	return m.RunFunc(traceLog, request)
}

type MockOptions struct {
	Option1 string `json:"option1"`
	Option2 int    `json:"option2"`
}

func (o MockOptions) Validate() error {
	return nil
}
