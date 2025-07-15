// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
)

type MockStep[RequestType scheduler.PipelineRequest] struct {
	Name     string
	Alias    string
	InitFunc func(alias string, db db.DB, opts conf.RawOpts) error
	RunFunc  func(traceLog *slog.Logger, request RequestType) (*scheduler.StepResult, error)
}

func (m *MockStep[RequestType]) GetName() string {
	return m.Name
}
func (m *MockStep[RequestType]) GetAlias() string {
	return m.Alias
}
func (m *MockStep[RequestType]) Init(alias string, db db.DB, opts conf.RawOpts) error {
	return m.InitFunc(alias, db, opts)
}
func (m *MockStep[RequestType]) Run(traceLog *slog.Logger, request RequestType) (*scheduler.StepResult, error) {
	return m.RunFunc(traceLog, request)
}
