// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

type mockStep struct {
	Name      string
	InitErr   error
	RunErr    error
	Decisions []string
}

func (m *mockStep) Init(_ db.DB, _ conf.RawOpts) error {
	return m.InitErr
}

func (m *mockStep) GetName() string {
	return m.Name
}

func (m *mockStep) Run() ([]string, error) {
	if m.RunErr != nil {
		return nil, m.RunErr
	}
	return m.Decisions, nil
}

type MockOptions struct {
	Option1 string `json:"option1"`
	Option2 int    `json:"option2"`
}

func (o MockOptions) Validate() error {
	return nil
}

type BaseStep struct {
	Options MockOptions
	DB      db.DB
}

func (s *BaseStep) Init(db db.DB, opts conf.RawOpts) error {
	s.DB = db
	// Use the actual unmarshal logic from conf.RawOpts
	if err := opts.Unmarshal(&s.Options); err != nil {
		return err
	}
	return s.Options.Validate()
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

	step := &BaseStep{}
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
