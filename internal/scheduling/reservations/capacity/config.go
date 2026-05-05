// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package capacity

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Config holds configuration for the capacity controller.
type Config struct {
	// ReconcileInterval is how often the controller probes the scheduler and updates CRDs.
	ReconcileInterval metav1.Duration `json:"capacityReconcileInterval"`

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
	if c.ReconcileInterval.Duration == 0 {
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
		ReconcileInterval: metav1.Duration{Duration: 5 * time.Minute},
		TotalPipeline:     "kvm-report-capacity",
		PlaceablePipeline: "kvm-general-purpose-load-balancing",
		SchedulerURL:      "http://localhost:8080/scheduler/nova/external",
	}
}
