// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
)

// Interface for a scheduler step.
type Step interface {
	// Configure the step with a database and options.
	Init(db db.DB, opts conf.RawOpts) error
	// Run this step of the scheduling pipeline.
	// Return a map of hostnames to activation values. Important: hosts that are
	// not in the map are considered as filtered out.
	Run(scenario Scenario) (map[string]float64, error)
	// Get the name of this step.
	// The name is used to identify the step in metrics, config, logs, and more.
	// Should be something like: "my_cool_scheduler_step".
	GetName() string
}

type ScenarioHost interface {
	// Get the name of the host.
	GetComputeHost() string
	// Get the hypervisor hostname of the host.
	GetHypervisorHostname() string
}

type Scenario interface {
	// Get the project ID of the VM to be scheduled.
	GetProjectID() string

	// Whether we are looking at a rebuild request.
	GetRebuild() bool
	// Whether we are looking at a resize request.
	GetResize() bool
	// Whether we are looking at a live migration.
	GetLive() bool
	// Whether the VM is a VMware VM.
	GetVMware() bool

	// Get the hosts in the state.
	GetHosts() []ScenarioHost
}
