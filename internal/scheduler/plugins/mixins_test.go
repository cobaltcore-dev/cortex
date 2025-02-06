// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"reflect"
	"testing"

	"gopkg.in/yaml.v2"
)

type TestStepOpts struct {
	IntOpt   int     `yaml:"intOpt"`
	FloatOpt float64 `yaml:"floatOpt"`
}

type TestStep struct {
	*StepMixin[TestStepOpts]
}

func TestStepMixin_LoadOpts(t *testing.T) {
	tests := []struct {
		name string
		opts yaml.MapSlice
		want TestStep
	}{
		{
			name: "AllOptionsProvided",
			opts: yaml.MapSlice{
				yaml.MapItem{Key: "intOpt", Value: 42},
				yaml.MapItem{Key: "floatOpt", Value: 3.14},
			},
			want: TestStep{
				&StepMixin[TestStepOpts]{Options: TestStepOpts{
					IntOpt:   42,
					FloatOpt: 3.14,
				}},
			},
		},
		{
			name: "TypeConversionIntToFloat",
			opts: yaml.MapSlice{
				yaml.MapItem{Key: "intOpt", Value: 42},
				yaml.MapItem{Key: "floatOpt", Value: 42},
			},
			want: TestStep{
				&StepMixin[TestStepOpts]{Options: TestStepOpts{
					IntOpt:   42,
					FloatOpt: 42.0,
				}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := TestStep{&StepMixin[TestStepOpts]{}}
			if err := step.LoadOpts(tt.opts); err != nil {
				t.Errorf("LoadOpts() error = %v", err)
				return
			}
			if !reflect.DeepEqual(step, tt.want) {
				t.Errorf("LoadOpts() = %v, want %v", step, tt.want)
			}
		})
	}
}
