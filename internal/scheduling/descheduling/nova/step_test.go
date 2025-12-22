// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type MockOptions struct {
	Option1 string `json:"option1"`
	Option2 int    `json:"option2"`
}

func (o MockOptions) Validate() error {
	return nil
}

type BaseStep struct {
	Options MockOptions
	DB      *db.DB
	Client  client.Client
}

func (s *BaseStep) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	s.DB = db
	s.Client = client
	// Use the actual unmarshal logic from conf.RawOpts
	if err := opts.Unmarshal(&s.Options); err != nil {
		return err
	}
	return s.Options.Validate()
}

func TestBaseStep_Init(t *testing.T) {
	opts := conf.NewRawOpts(`{
        "option1": "value1",
        "option2": 2
    }`)

	step := &BaseStep{}
	err := step.Init(nil, nil, opts)
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
