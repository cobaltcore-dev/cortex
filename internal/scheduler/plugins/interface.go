// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"log/slog"
	"math"

	"github.com/cobaltcore-dev/cortex/internal/db"
)

// Interface for a scheduler step.
type Step interface {
	// Configure the step with a database and options.
	Init(db db.DB, opts map[string]any) error
	// Run this step of the scheduling pipeline.
	// The step receives a state object which contains hosts and weights
	// and can modify these weights and hosts as needed. The state object
	// is then passed to the next scheduler step. Thus, it is important
	// to keep a clean shop and make certain that, if hosts are removed
	// from the state, their weights are removed as well.
	Run(state *State) error
	// Get the name of this step.
	// The name is used to identify the step in metrics, config, logs, and more.
	// Should be something like: "my_cool_scheduler_step".
	GetName() string
}

// State passed between scheduler steps.
type State struct {
	Spec    StateSpec
	Hosts   []StateHost
	Weights map[string]float64
}

// Apply a tanh activation function to all weights in the state.
// See: https://en.wikipedia.org/wiki/Activation_function
func (state *State) ScaleNovaValues() {
	for hostname, weight := range state.Weights {
		state.Weights[hostname] = math.Tanh(weight)
	}
}

// Vote the a hostname in the state.
//
// The passed activation value can be everything between -float64_max and
// float64_max. Activations are applied to previous weights using a tanh
// activation function. See: https://en.wikipedia.org/wiki/Activation_function
func (state *State) Vote(hostname string, activation float64) {
	// Check if the hostname is in the state.
	if _, ok := state.Weights[hostname]; !ok {
		slog.Warn(
			"attempted to vote unknown host",
			"hostname", hostname,
		)
		return
	}
	// Check if the weight is present.
	prevWeight, ok := state.Weights[hostname]
	if !ok {
		slog.Warn(
			"attempted to vote host with missing weight",
			"hostname", hostname,
		)
		return
	}
	// Apply the hyperbolic tangent activation function.
	state.Weights[hostname] = prevWeight + math.Tanh(activation)
}

type StateSpec struct {
	ProjectID string
}

type StateHost struct {
	// Name of the Nova compute host, e.g. nova-compute-bb123.
	ComputeHost string
	// Name of the hypervisor hostname, e.g. domain-c123.<uuid>
	HypervisorHostname string
}
