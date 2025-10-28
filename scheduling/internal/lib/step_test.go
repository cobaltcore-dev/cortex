// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

type mockStep[RequestType PipelineRequest] struct {
	Name     string
	InitFunc func(db db.DB, opts conf.RawOpts) error
	RunFunc  func(traceLog *slog.Logger, request RequestType) (*StepResult, error)
}

func (m *mockStep[RequestType]) GetName() string { return m.Name }
func (m *mockStep[RequestType]) Init(db db.DB, opts conf.RawOpts) error {
	return m.InitFunc(db, opts)
}
func (m *mockStep[RequestType]) Run(traceLog *slog.Logger, request RequestType) (*StepResult, error) {
	return m.RunFunc(traceLog, request)
}

type MockOptions struct {
	Option1 string `json:"option1"`
	Option2 int    `json:"option2"`
}

func (o MockOptions) Validate() error {
	return nil
}

func TestBaseStep_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	opts := conf.NewRawOpts(`{
        "option1": "value1",
        "option2": 2
    }`)

	step := BaseStep[mockPipelineRequest, MockOptions]{}
	err := step.Init(testDB, opts)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if step.Options.Option1 != "value1" {
		t.Errorf("expected Option1 to be 'value1', got %s", step.Options.Option1)
	}

	if step.Options.Option2 != 2 {
		t.Errorf("expected Option2 to be 2, got %d", step.Options.Option2)
	}
}
