// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import "encoding/json"

// Raw options that are not directly unmarshalled when loading from json.
// Usage: call Unmarshal to unmarshal the options into a struct.
type RawOpts struct {
	// Postponed unmarshal function.
	unmarshal func(any) error
}

// Create a new RawOpts instance with the given json string.
func NewRawOpts(rawJson string) RawOpts {
	return RawOpts{unmarshal: func(v any) error {
		return json.Unmarshal([]byte(rawJson), v)
	}}
}

// Call the postponed unmarshal function and unmarshal the options into a struct.
func (msg *RawOpts) Unmarshal(v any) error {
	if msg.unmarshal == nil {
		// No unmarshal function set (e.g. empty json), return nil.
		return nil
	}
	return msg.unmarshal(v)
}

// Override the default json unmarshal behavior to postpone the unmarshal.
func (msg *RawOpts) UnmarshalJSON(unmarshal func(any) error) error {
	msg.unmarshal = unmarshal
	return nil
}

// Mixin that adds the ability to load options from a json map.
// Usage: type StructUsingOpts struct { conf.JsonOpts[MyOpts] }
type JsonOpts[Options any] struct {
	// Options loaded from a json config using the Load method.
	Options Options
}

// Set the options contained in the opts json map.
func (s *JsonOpts[Options]) Load(opts RawOpts) error {
	var o Options
	if err := opts.Unmarshal(&o); err != nil {
		return err
	}
	s.Options = o
	return nil
}
