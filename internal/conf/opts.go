// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import "gopkg.in/yaml.v3"

// Raw options that are not directly unmarshalled when loading from yaml.
// Usage: call Unmarshal to unmarshal the options into a struct.
type RawOpts struct {
	// Postponed unmarshal function.
	unmarshal func(any) error
}

// Create a new RawOpts instance with the given yaml string.
func NewRawOpts(rawYaml string) RawOpts {
	return RawOpts{unmarshal: func(v any) error {
		return yaml.Unmarshal([]byte(rawYaml), v)
	}}
}

// Call the postponed unmarshal function and unmarshal the options into a struct.
func (msg *RawOpts) Unmarshal(v any) error { return msg.unmarshal(v) }

// Override the default yaml unmarshal behavior to postpone the unmarshal.
func (msg *RawOpts) UnmarshalYAML(unmarshal func(any) error) error {
	msg.unmarshal = unmarshal
	return nil
}

// Mixin that adds the ability to load options from a yaml map.
// Usage: type StructUsingOpts struct { conf.YamlOpts[MyOpts] }
type YamlOpts[Options any] struct {
	// Options loaded from a yaml config using the Load method.
	Options Options
}

// Set the options contained in the opts yaml map.
func (s *YamlOpts[Options]) Load(opts RawOpts) error {
	var o Options
	if err := opts.Unmarshal(&o); err != nil {
		return err
	}
	s.Options = o
	return nil
}
