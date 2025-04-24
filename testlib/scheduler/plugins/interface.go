// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/api"
)

// MockStep is a manual mock implementation of the plugins.Step interface.
type MockStep struct {
	Name     string
	InitFunc func(db db.DB, opts conf.RawOpts) error
	RunFunc  func(request api.Request) (map[string]float64, error)
}

func (m *MockStep) GetName() string {
	return m.Name
}

func (m *MockStep) Init(db db.DB, opts conf.RawOpts) error {
	return m.InitFunc(db, opts)
}

func (m *MockStep) Run(request api.Request) (map[string]float64, error) {
	return m.RunFunc(request)
}
