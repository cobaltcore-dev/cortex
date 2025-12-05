// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	testlibDB "github.com/cobaltcore-dev/cortex/pkg/db/testing"
	"k8s.io/apimachinery/pkg/runtime"
)

type MockOptions struct {
	Option1 string `json:"option1"`
	Option2 int    `json:"option2"`
}

type MockDatasource struct {
	ID   int    `db:"id,primarykey"`
	Name string `db:"name"`
}

func (md MockDatasource) TableName() string {
	return "mock_datasource"
}

func (md MockDatasource) Indexes() map[string][]string {
	return map[string][]string{}
}

type MockFeature struct {
	ID   int    `db:"id,primarykey"`
	Name string `db:"name"`
}

func TestBaseExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	opts := []byte(`{
        "option1": "value1",
        "option2": 2
    }`)

	config := v1alpha1.KnowledgeSpec{
		Extractor: v1alpha1.KnowledgeExtractorSpec{
			Name:   "mock_extractor",
			Config: runtime.RawExtension{Raw: opts},
		},
	}

	extractor := BaseExtractor[MockOptions, MockFeature]{}
	err := extractor.Init(&testDB, nil, config)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if extractor.Options.Option1 != "value1" {
		t.Errorf("expected Option1 to be 'value1', got %s", extractor.Options.Option1)
	}

	if extractor.Options.Option2 != 2 {
		t.Errorf("expected Option2 to be 2, got %d", extractor.Options.Option2)
	}
}

func TestBaseExtractor_Extracted(t *testing.T) {
	extractor := BaseExtractor[MockOptions, MockFeature]{}

	// Insert mock data into the mock_feature table
	mockFeatures := []MockFeature{
		{ID: 1, Name: "feature1"},
		{ID: 2, Name: "feature2"},
	}

	// Call the Extracted function
	features, err := extractor.Extracted(mockFeatures)
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
		f := f.(MockFeature)
		if expected[f.ID] != f.Name {
			t.Errorf("expected name for ID %d to be %s, got %s", f.ID, expected[f.ID], f.Name)
		}
	}
}

func TestBaseExtractor_ExtractSQL(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	// Create the table for MockDatasource
	err := testDB.CreateTable(testDB.AddTable(MockDatasource{}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the mock_feature table
	mockDs := []MockDatasource{
		{ID: 1, Name: "feature1"},
		{ID: 2, Name: "feature2"},
	}
	for _, d := range mockDs {
		if err := testDB.Insert(&d); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	}

	extractor := BaseExtractor[MockOptions, MockFeature]{DB: &testDB}

	// Define the SQL query to extract features
	table := MockDatasource{}.TableName()
	query := "SELECT * FROM " + table

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
