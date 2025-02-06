// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"github.com/cobaltcore-dev/cortex/internal/db"
	"gopkg.in/yaml.v2"
)

// Mixin that can be embedded in a step to provide some common tooling.
type StepMixin[O any] struct {
	DB      db.DB
	Options O
}

func (s *StepMixin[O]) Init(db db.DB, opts yaml.MapSlice) error {
	s.DB = db
	return s.LoadOpts(opts)
}

// Get the base activations for all hosts in the scenario.
func (s *StepMixin[O]) GetBaseActivations(scenario Scenario) map[string]float64 {
	weights := make(map[string]float64)
	for _, host := range scenario.GetHosts() {
		// No change in weight (tanh(0.0) = 0.0).
		weights[host.GetComputeHost()] = 0.0
	}
	return weights
}

// Set the options contained in the opts yaml map.
func (s *StepMixin[O]) LoadOpts(opts yaml.MapSlice) error {
	bytes, err := yaml.Marshal(opts)
	if err != nil {
		return err
	}
	var o O
	if err := yaml.UnmarshalStrict(bytes, &o); err != nil {
		return err
	}
	s.Options = o
	return nil
}
