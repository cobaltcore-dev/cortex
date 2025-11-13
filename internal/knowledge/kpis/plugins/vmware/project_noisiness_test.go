// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/vmware"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	testlibDB "github.com/cobaltcore-dev/cortex/pkg/db/testing"
	"github.com/prometheus/client_golang/prometheus"
)

func TestVMwareProjectNoisinessKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	kpi := &VMwareProjectNoisinessKPI{}
	if err := kpi.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestVMwareProjectNoisinessKPI_Collect(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(vmware.VROpsProjectNoisiness{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	vropsProjectNoisiness := []any{
		&vmware.VROpsProjectNoisiness{Project: "project1", ComputeHost: "host1", AvgCPUOfProject: 10.5},
		&vmware.VROpsProjectNoisiness{Project: "project2", ComputeHost: "host2", AvgCPUOfProject: 15.0},
	}
	if err := testDB.Insert(vropsProjectNoisiness...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &VMwareProjectNoisinessKPI{}
	if err := kpi.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan prometheus.Metric, 10)
	kpi.Collect(ch)
	close(ch)

	metricsCount := 0
	for range ch {
		metricsCount++
	}

	if metricsCount == 0 {
		t.Errorf("expected metrics to be collected, got %d", metricsCount)
	}
}
