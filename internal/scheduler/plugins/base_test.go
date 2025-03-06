// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

type MockOptions struct {
	Option1 string `yaml:"option1"`
	Option2 int    `yaml:"option2"`
}

func (o MockOptions) Validate() error {
	return nil
}

func TestBaseStep_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	opts := conf.NewRawOpts(`
        option1: value1
        option2: 2
    `)

	step := BaseStep[MockOptions]{}
	err := step.Init(testDB, opts)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if step.YamlOpts.Options.Option1 != "value1" {
		t.Errorf("expected Option1 to be 'value1', got %s", step.YamlOpts.Options.Option1)
	}

	if step.YamlOpts.Options.Option2 != 2 {
		t.Errorf("expected Option2 to be 2, got %d", step.YamlOpts.Options.Option2)
	}
}
