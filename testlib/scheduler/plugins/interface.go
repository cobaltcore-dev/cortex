// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/plugins"
)

// MockStep is a manual mock implementation of the plugins.Step interface.
type MockStep struct {
	Name     string
	InitFunc func(db db.DB, opts conf.RawOpts) error
	RunFunc  func(traceLog *slog.Logger, request api.Request) (*plugins.StepResult, error)
}

func (m *MockStep) GetName() string {
	return m.Name
}

func (m *MockStep) Init(db db.DB, opts conf.RawOpts) error {
	return m.InitFunc(db, opts)
}

func (m *MockStep) Run(traceLog *slog.Logger, request api.Request) (*plugins.StepResult, error) {
	return m.RunFunc(traceLog, request)
}
