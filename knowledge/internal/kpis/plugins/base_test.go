// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

type MockOptions struct {
	Option1 string `yaml:"option1"`
	Option2 int    `yaml:"option2"`
}

type MockKPI struct {
	BaseKPI[MockOptions]
	ID   int    `db:"id,primarykey"`
	Name string `db:"name"`
}

func (MockKPI) TableName() string {
	return "mock_kpi"
}

func TestBaseKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	opts := conf.NewRawOpts(`{
        "option1": "value1",
        "option2": 2
    }`)
	baseKPI := MockKPI{}
	err := baseKPI.Init(testDB, opts)
	if err != nil {
		t.Errorf("Init() failed: %v", err)
	}

	if baseKPI.Options.Option1 != "value1" {
		t.Errorf("expected Option1 to be 'value1', got %s", baseKPI.Options.Option1)
	}
	if baseKPI.Options.Option2 != 2 {
		t.Errorf("expected Option2 to be 2, got %d", baseKPI.Options.Option2)
	}
}
