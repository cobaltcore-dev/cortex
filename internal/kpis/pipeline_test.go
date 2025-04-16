// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kpis

import (
	"errors"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/prometheus/client_golang/prometheus"
)

type mockKPI struct {
	name    string
	initErr error
}

func (m *mockKPI) GetName() string {
	return m.name
}

func (m *mockKPI) Init(db db.DB, opts conf.RawOpts) error {
	return m.initErr
}

func (m *mockKPI) Collect(ch chan<- prometheus.Metric) {
	// Mock implementation
}
func (m *mockKPI) Describe(ch chan<- *prometheus.Desc) {
	// Mock implementation
}

func TestKPIPipeline_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()
	registry := monitoring.NewRegistry(conf.MonitoringConfig{
		Labels: map[string]string{"env": "test"},
	})

	mockKPI1 := &mockKPI{name: "mock_kpi_1"}
	mockKPI2 := &mockKPI{name: "mock_kpi_2", initErr: errors.New("init error")}

	config := conf.KPIsConfig{
		Plugins: []conf.KPIPluginConfig{
			{Name: "mock_kpi_1", Options: conf.RawOpts{}},
			{Name: "mock_kpi_2", Options: conf.RawOpts{}},
		},
	}

	pipeline := NewPipeline(config)

	err := pipeline.Init([]plugins.KPI{mockKPI1, mockKPI2}, testDB, registry)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	expectedError := "failed to initialize kpi mock_kpi_2: init error"
	if err.Error() != expectedError {
		t.Errorf("expected error %q, got %q", expectedError, err.Error())
	}
}
