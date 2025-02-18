// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"gopkg.in/yaml.v3"
)

// Mixin that adds the ability to load options from a yaml map.
// Usage: type StructUsingOpts struct { conf.YamlOpts[MyOpts] }
type YamlOpts[Options any] struct {
	// Options loaded from a yaml config using the Load method.
	Options Options
}

// Set the options contained in the opts yaml map.
func (s *YamlOpts[Options]) Load(opts yaml.MapSlice) error {
	bytes, err := yaml.Marshal(opts)
	if err != nil {
		return err
	}
	var o Options
	if err := yaml.UnmarshalStrict(bytes, &o); err != nil {
		return err
	}
	s.Options = o
	return nil
}
