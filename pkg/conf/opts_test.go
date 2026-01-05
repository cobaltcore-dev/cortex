// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"testing"
)

type MockOptions struct {
	Option1 string `json:"option1"`
	Option2 int    `json:"option2"`
}

func TestJsonOpts(t *testing.T) {
	opts := NewRawOpts(`{
        "option1": "value1",
        "option2": 2
    }`)

	jsonOpts := JsonOpts[MockOptions]{}
	err := jsonOpts.Load(opts)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if jsonOpts.Options.Option1 != "value1" {
		t.Errorf("expected option1 to be 'value1', got %v", jsonOpts.Options.Option1)
	}
	if jsonOpts.Options.Option2 != 2 {
		t.Errorf("expected option2 to be 2, got %v", jsonOpts.Options.Option2)
	}
}
