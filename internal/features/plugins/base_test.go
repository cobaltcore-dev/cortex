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
	Option1 string `json:"option1"`
	Option2 int    `json:"option2"`
}

type MockFeature struct {
	ID   int    `db:"id,primarykey"`
	Name string `db:"name"`
}

func (MockFeature) TableName() string {
	return "mock_feature"
}

func (MockFeature) Indexes() []db.Index {
	return nil
}

func TestBaseExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	opts := conf.NewRawOpts(`{
        "option1": "value1",
        "option2": 2
    }`)

	extractor := BaseExtractor[MockOptions, MockFeature]{}
	err := extractor.Init(testDB, opts)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if extractor.Options.Option1 != "value1" {
		t.Errorf("expected Option1 to be 'value1', got %s", extractor.Options.Option1)
	}

	if extractor.Options.Option2 != 2 {
		t.Errorf("expected Option2 to be 2, got %d", extractor.Options.Option2)
	}

	if !testDB.TableExists(MockFeature{}) {
		t.Fatal("expected table to exist")
	}
}

func TestBaseExtractor_Extracted(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create the table for MockFeature
	err := testDB.CreateTable(testDB.AddTable(MockFeature{}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	extractor := BaseExtractor[MockOptions, MockFeature]{DB: testDB}

	// Insert mock data into the mock_feature table
	mockFeatures := []MockFeature{
		{ID: 1, Name: "feature1"},
		{ID: 2, Name: "feature2"},
	}

	// Call the Extracted function
	extractedFeatures, err := extractor.Extracted(mockFeatures)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the data was replaced in the mock_feature table
	var features []MockFeature
	_, err = testDB.Select(&features, "SELECT * FROM mock_feature")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(features) != 2 {
		t.Errorf("expected 2 rows, got %d", len(features))
	}

	expected := map[int]string{
		1: "feature1",
		2: "feature2",
	}
	for _, f := range features {
		if expected[f.ID] != f.Name {
			t.Errorf("expected name for ID %d to be %s, got %s", f.ID, expected[f.ID], f.Name)
		}
	}

	// Verify the returned slice of features
	if len(extractedFeatures) != 2 {
		t.Errorf("expected 2 extracted features, got %d", len(extractedFeatures))
	}
	for i, f := range extractedFeatures {
		if f.(MockFeature).ID != mockFeatures[i].ID || f.(MockFeature).Name != mockFeatures[i].Name {
			t.Errorf("expected extracted feature %v, got %v", mockFeatures[i], f)
		}
	}
}

func TestBaseExtractor_ExtractSQL(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create the table for MockFeature
	err := testDB.CreateTable(testDB.AddTable(MockFeature{}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the mock_feature table
	mockFeatures := []MockFeature{
		{ID: 1, Name: "feature1"},
		{ID: 2, Name: "feature2"},
	}
	for _, feature := range mockFeatures {
		if err := testDB.Insert(&feature); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	}

	extractor := BaseExtractor[MockOptions, MockFeature]{DB: testDB}

	// Define the SQL query to extract features
	query := "SELECT * FROM mock_feature"

	// Call the ExtractSQL function
	extractedFeatures, err := extractor.ExtractSQL(query)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the returned slice of features
	if len(extractedFeatures) != 2 {
		t.Errorf("expected 2 extracted features, got %d", len(extractedFeatures))
	}
	expected := map[int]string{
		1: "feature1",
		2: "feature2",
	}
	// Correctly cast the generic Feature type to MockFeature
	for _, f := range extractedFeatures {
		mockFeature, ok := f.(MockFeature)
		if !ok {
			t.Fatalf("expected type MockFeature, got %T", f)
		}
		if expected[mockFeature.ID] != mockFeature.Name {
			t.Errorf("expected name for ID %d to be %s, got %s", mockFeature.ID, expected[mockFeature.ID], mockFeature.Name)
		}
	}
}
