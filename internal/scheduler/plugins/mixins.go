// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"github.com/cobaltcore-dev/cortex/internal/db"
	"gopkg.in/yaml.v2"
)

// Mixin that can be embedded in a step to provide some common tooling.
type BaseStep[Options any] struct {
	DB      db.DB
	Options Options
}

func (s *BaseStep[Options]) Init(db db.DB, opts yaml.MapSlice) error {
	s.DB = db
	return s.LoadOpts(opts)
}

// Get activations that will have no effect on the scenario, for all hosts.
// This function can be used to first fill up the activations map and then
// only apply changes for the hosts that are relevant.
func (s *BaseStep[Options]) GetNoEffectActivations(scenario Scenario) map[string]float64 {
	weights := make(map[string]float64)
	for _, host := range scenario.GetHosts() {
		// No change in weight (tanh(0.0) = 0.0).
		weights[host.GetComputeHost()] = 0.0
	}
	return weights
}

// Set the options contained in the opts yaml map.
func (s *BaseStep[Options]) LoadOpts(opts yaml.MapSlice) error {
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
