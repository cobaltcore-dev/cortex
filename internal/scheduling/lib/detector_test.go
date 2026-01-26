// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type mockDetector[RequestType PipelineRequest] struct {
	InitFunc func(ctx context.Context, client client.Client, step v1alpha1.DetectorSpec) error
	RunFunc  func(traceLog *slog.Logger, request RequestType) (*StepResult, error)
}

func (m *mockDetector[RequestType]) Init(ctx context.Context, client client.Client, step v1alpha1.DetectorSpec) error {
	return m.InitFunc(ctx, client, step)
}
func (m *mockDetector[RequestType]) Run(traceLog *slog.Logger, request RequestType) (*StepResult, error) {
	return m.RunFunc(traceLog, request)
}
