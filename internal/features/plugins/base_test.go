// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"testing"

	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"gopkg.in/yaml.v2"
)

type MockOptions struct {
	Option1 string `yaml:"option1"`
	Option2 int    `yaml:"option2"`
}

type MockFeature struct {
	ID   int    `db:"id,primarykey"`
	Name string `db:"name"`
}

func (MockFeature) TableName() string {
	return "mock_feature"
}

func TestBaseExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	defer dbEnv.Close()
	testDB := dbEnv.DB

	opts := yaml.MapSlice{
		{Key: "option1", Value: "value1"},
		{Key: "option2", Value: 2},
	}

	extractor := BaseExtractor[MockOptions, MockFeature]{}
	err := extractor.Init(*testDB, opts)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if extractor.YamlOpts.Options.Option1 != "value1" {
		t.Errorf("expected Option1 to be 'value1', got %s", extractor.YamlOpts.Options.Option1)
	}

	if extractor.YamlOpts.Options.Option2 != 2 {
		t.Errorf("expected Option2 to be 2, got %d", extractor.YamlOpts.Options.Option2)
	}

	if !testDB.TableExists(MockFeature{}) {
		t.Fatal("expected table to exist")
	}
}
