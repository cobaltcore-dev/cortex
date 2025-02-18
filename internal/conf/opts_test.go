// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"testing"

	"gopkg.in/yaml.v3"
)

type MockOptions struct {
	Option1 string `yaml:"option1"`
	Option2 int    `yaml:"option2"`
}

func TestYamlOpts(t *testing.T) {
	opts := yaml.MapSlice{
		{Key: "option1", Value: "value1"},
		{Key: "option2", Value: 2},
	}

	yamlOpts := YamlOpts[MockOptions]{}
	err := yamlOpts.Load(opts)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if yamlOpts.Options.Option1 != "value1" {
		t.Errorf("expected option1 to be 'value1', got %v", yamlOpts.Options.Option1)
	}
	if yamlOpts.Options.Option2 != 2 {
		t.Errorf("expected option2 to be 2, got %v", yamlOpts.Options.Option2)
	}
}
