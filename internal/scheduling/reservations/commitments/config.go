// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"time"

	corev1 "k8s.io/api/core/v1"
)

type Config struct {

	// RequeueIntervalActive is the interval for requeueing active reservations for periodic verification.
	RequeueIntervalActive time.Duration `json:"committedResourceRequeueIntervalActive"`
	// RequeueIntervalRetry is the interval for requeueing when retrying after knowledge is not ready.
	RequeueIntervalRetry time.Duration `json:"committedResourceRequeueIntervalRetry"`
	// AllocationGracePeriod is the time window after a VM is allocated to a reservation
	// during which it's expected to appear on the target host. VMs not confirmed within
	// this period are considered stale and removed from the reservation.
	AllocationGracePeriod time.Duration `json:"committedResourceAllocationGracePeriod"`
	// RequeueIntervalGracePeriod is the interval for requeueing when VMs are in grace period.
	// Shorter than RequeueIntervalActive for faster verification of new allocations.
	RequeueIntervalGracePeriod time.Duration `json:"committedResourceRequeueIntervalGracePeriod"`
	// PipelineDefault is the default pipeline used for scheduling committed resource reservations.
	PipelineDefault string `json:"committedResourcePipelineDefault"`

	// SchedulerURL is the endpoint of the nova external scheduler
	SchedulerURL string `json:"schedulerURL"`

	// Secret ref to the database credentials for querying VM state.
	DatabaseSecretRef *corev1.SecretReference `json:"databaseSecretRef,omitempty"`

	// ReportCapacityTotalPipeline is the pipeline used to determine eligible hosts for capacity calculation.
	// This pipeline ignores VM allocations and reservations (empty datacenter scenario).
	// Host resource data is then read from Hypervisor CRDs to compute actual multiples.
	ReportCapacityTotalPipeline string `json:"reportCapacityTotalPipeline"`

	// FlavorGroupPipelines maps flavor group names to pipeline names.
	// Example: {"2152": "kvm-hana-bin-packing", "2101": "kvm-general-purpose-load-balancing", "*": "kvm-general-purpose-load-balancing"}
	// Used to select different scheduling pipelines based on flavor group characteristics.
	FlavorGroupPipelines map[string]string `json:"committedResourceFlavorGroupPipelines,omitempty"`

	// API configuration

	// ChangeAPIWatchReservationsTimeout defines how long to wait for reservations to become ready before timing out and rolling back.
	ChangeAPIWatchReservationsTimeout time.Duration `json:"committedResourceChangeAPIWatchReservationsTimeout"`

	// ChangeAPIWatchReservationsPollInterval defines how frequently to poll reservation status during watch.
	ChangeAPIWatchReservationsPollInterval time.Duration `json:"committedResourceChangeAPIWatchReservationsPollInterval"`

	// EnableChangeCommitmentsAPI controls whether the change-commitments API endpoint is active.
	// When false, the endpoint will return HTTP 503 Service Unavailable.
	// The info endpoint remains available for health checks.
	EnableChangeCommitmentsAPI bool `json:"committedResourceEnableChangeCommitmentsAPI"`

	// EnableReportUsageAPI controls whether the report-usage API endpoint is active.
	// When false, the endpoint will return HTTP 503 Service Unavailable.
	// This can be used as an emergency switch if the usage reporting is causing issues.
	EnableReportUsageAPI bool `json:"committedResourceEnableReportUsageAPI"`

	// EnableReportCapacityAPI controls whether the report-capacity API endpoint is active.
	// When false, the endpoint will return HTTP 503 Service Unavailable.
	// This can be used as an emergency switch if the capacity reporting is causing issues.
	EnableReportCapacityAPI bool `json:"committedResourceEnableReportCapacityAPI"`
}

// ApplyDefaults fills in any unset values with defaults.
func (c *Config) ApplyDefaults() {
	defaults := DefaultConfig()
	if c.RequeueIntervalActive == 0 {
		c.RequeueIntervalActive = defaults.RequeueIntervalActive
	}
	if c.RequeueIntervalRetry == 0 {
		c.RequeueIntervalRetry = defaults.RequeueIntervalRetry
	}
	if c.RequeueIntervalGracePeriod == 0 {
		c.RequeueIntervalGracePeriod = defaults.RequeueIntervalGracePeriod
	}
	if c.AllocationGracePeriod == 0 {
		c.AllocationGracePeriod = defaults.AllocationGracePeriod
	}
	if c.PipelineDefault == "" {
		c.PipelineDefault = defaults.PipelineDefault
	}
	if c.SchedulerURL == "" {
		c.SchedulerURL = defaults.SchedulerURL
	}
	if c.ChangeAPIWatchReservationsTimeout == 0 {
		c.ChangeAPIWatchReservationsTimeout = defaults.ChangeAPIWatchReservationsTimeout
	}
	if c.ChangeAPIWatchReservationsPollInterval == 0 {
		c.ChangeAPIWatchReservationsPollInterval = defaults.ChangeAPIWatchReservationsPollInterval
	}
	// Note: EnableChangeCommitmentsAPI, EnableReportUsageAPI, EnableReportCapacityAPI
	// are booleans where false is a valid value, so we don't apply defaults for them
}

func DefaultConfig() Config {
	return Config{
		RequeueIntervalActive:                  5 * time.Minute,
		RequeueIntervalRetry:                   1 * time.Minute,
		RequeueIntervalGracePeriod:             1 * time.Minute,
		AllocationGracePeriod:                  15 * time.Minute,
		PipelineDefault:                        "kvm-general-purpose-load-balancing",
		SchedulerURL:                           "http://localhost:8080/scheduler/nova/external",
		ReportCapacityTotalPipeline:            "kvm-report-capacity",
		ChangeAPIWatchReservationsTimeout:      10 * time.Second,
		ChangeAPIWatchReservationsPollInterval: 500 * time.Millisecond,
		EnableChangeCommitmentsAPI:             true,
		EnableReportUsageAPI:                   true,
		EnableReportCapacityAPI:                true,
	}
}
