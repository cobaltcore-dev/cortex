// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"testing"

	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"gopkg.in/yaml.v2"
)

type MockTable struct {
	ID   int
	Name string
}

type MockOptions struct {
	Option1 string `yaml:"option1"`
	Option2 int    `yaml:"option2"`
}

func TestBaseExtractor_Init(t *testing.T) {
	mockDB := testlibDB.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	opts := yaml.MapSlice{
		{Key: "option1", Value: "value1"},
		{Key: "option2", Value: 2},
	}

	extractor := BaseExtractor[MockTable, MockOptions]{}
	err := extractor.Init(&mockDB, opts)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if extractor.Options.Option1 != "value1" {
		t.Errorf("expected option1 to be 'value1', got %v", extractor.Options.Option1)
	}
	if extractor.Options.Option2 != 2 {
		t.Errorf("expected option2 to be 2, got %v", extractor.Options.Option2)
	}
	if extractor.DB == nil {
		t.Errorf("expected DB to be initialized, got nil")
	}
}

func TestBaseExtractor_LoadOpts(t *testing.T) {
	mockDB := testlibDB.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	opts := yaml.MapSlice{
		{Key: "option1", Value: "value1"},
		{Key: "option2", Value: 2},
	}

	extractor := BaseExtractor[MockTable, MockOptions]{DB: &mockDB}
	err := extractor.LoadOpts(opts)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if extractor.Options.Option1 != "value1" {
		t.Errorf("expected option1 to be 'value1', got %v", extractor.Options.Option1)
	}
	if extractor.Options.Option2 != 2 {
		t.Errorf("expected option2 to be 2, got %v", extractor.Options.Option2)
	}
}
