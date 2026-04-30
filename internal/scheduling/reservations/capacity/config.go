// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package capacity

import "time"

// Config holds configuration for the capacity controller.
type Config struct {
	// ReconcileInterval is how often the controller probes the scheduler and updates CRDs.
	ReconcileInterval time.Duration `json:"capacityReconcileInterval"`

	// TotalPipeline is the scheduler pipeline used for the empty-state probe.
	// This pipeline should ignore current VM allocations (e.g. kvm-report-capacity).
	TotalPipeline string `json:"capacityTotalPipeline"`

	// PlaceablePipeline is the scheduler pipeline used for the current-state probe.
	// This pipeline considers current VM allocations to determine remaining placement capacity.
	PlaceablePipeline string `json:"capacityPlaceablePipeline"`

	// SchedulerURL is the endpoint of the nova external scheduler.
	SchedulerURL string `json:"schedulerURL"`
}

// ApplyDefaults fills in any unset values with defaults.
func (c *Config) ApplyDefaults() {
	defaults := DefaultConfig()
	if c.ReconcileInterval == 0 {
		c.ReconcileInterval = defaults.ReconcileInterval
	}
	if c.TotalPipeline == "" {
		c.TotalPipeline = defaults.TotalPipeline
	}
	if c.PlaceablePipeline == "" {
		c.PlaceablePipeline = defaults.PlaceablePipeline
	}
	if c.SchedulerURL == "" {
		c.SchedulerURL = defaults.SchedulerURL
	}
}

func DefaultConfig() Config {
	return Config{
		ReconcileInterval: 5 * time.Minute,
		TotalPipeline:     "kvm-report-capacity",
		PlaceablePipeline: "kvm-general-purpose-load-balancing",
		SchedulerURL:      "http://localhost:8080/scheduler/nova/external",
	}
}
