// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FailoverConfig defines the configuration for failover reservation management.
type FailoverConfig struct {
	// FlavorFailoverRequirements maps flavor name patterns to required failover count.
	// Example: {"hana_*": 2, "m1.xlarge": 1}
	// A VM with a matching flavor will need this many failover reservations.
	FlavorFailoverRequirements map[string]int `json:"flavorFailoverRequirements"`

	// ReconcileInterval is how often to check for missing failover reservations.
	// Supports Go duration strings like "30s", "1m", "15m".
	ReconcileInterval metav1.Duration `json:"reconcileInterval"`

	// Creator tag for failover reservations (for identification and cleanup).
	Creator string `json:"creator"`

	// DatasourceName is the name of the Datasource CRD that provides database connection info.
	// This is used to read VM data from the Nova database.
	DatasourceName string `json:"datasourceName"`

	// SchedulerURL is the URL of the nova external scheduler API.
	// Example: "http://localhost:8080/scheduler/nova/external"
	SchedulerURL string `json:"schedulerURL"`

	// MaxVMsToProcess limits the number of VMs to process per reconciliation cycle.
	// Set to negative to process all VMs (default behavior).
	// Useful for debugging and testing with large VM counts.
	MaxVMsToProcess int `json:"maxVMsToProcess"`

	// ShortReconcileInterval is used when MaxVMsToProcess limits processing.
	// This allows faster catch-up when there are more VMs to process.
	// Set to 0 to use ReconcileInterval (default behavior).
	// Supports Go duration strings like "100ms", "1s", "1m".
	ShortReconcileInterval metav1.Duration `json:"shortReconcileInterval"`

	// MinSuccessForShortInterval is the minimum number of successful reservations (created + reused)
	// required to use ShortReconcileInterval. Default: 1. Use 0 to require no minimum.
	MinSuccessForShortInterval *int `json:"minSuccessForShortInterval"`

	// MaxFailuresForShortInterval is the maximum number of failures allowed to still use
	// ShortReconcileInterval. Default: 99. Use 0 to allow no failures.
	MaxFailuresForShortInterval *int `json:"maxFailuresForShortInterval"`

	// TrustHypervisorLocation when true, uses the hypervisor CRD as the source of truth
	// for VM location instead of postgres (OSEXTSRVATTRHost). This is useful when there
	// are data sync issues between nova and the hypervisor operator.
	// When enabled:
	// - VM location comes from hypervisor CRD (which hypervisor lists the VM in its instances)
	// - VM size/flavor still comes from postgres (needed for scheduling)
	// Default: false (use postgres OSEXTSRVATTRHost for location)
	TrustHypervisorLocation bool `json:"trustHypervisorLocation"`

	// RevalidationInterval is how often to re-validate acknowledged failover reservations.
	// After a reservation is acknowledged, it will be re-validated after this interval
	// to ensure the reservation host is still valid for all allocated VMs.
	// Default: 30 minutes
	// Supports Go duration strings like "15m", "30m", "1h".
	RevalidationInterval metav1.Duration `json:"revalidationInterval"`

	// LimitOneNewReservationPerHypervisor when true, prevents creating multiple new
	// reservations on the same hypervisor within a single reconcile cycle.
	// This helps spread reservations across hypervisors.
	// Default: true
	LimitOneNewReservationPerHypervisor bool `json:"limitOneNewReservationPerHypervisor"`

	// VMSelectionRotationInterval controls how often the VM selection offset rotates
	// when MaxVMsToProcess limits processing. Every N reconcile cycles, the offset
	// rotates to process different VMs. This ensures all VMs eventually get processed.
	// Default: 4 (rotate every 4th reconcile cycle). Use 0 to disable rotation.
	VMSelectionRotationInterval *int `json:"vmSelectionRotationInterval"`

	// UseFlavorGroupResources when true, sizes failover reservation resources based on
	// the LargestFlavor in the VM's flavor group instead of the VM's actual resources.
	// This enables better sharing: a single reservation can accommodate any flavor in the
	// group since it's sized for the largest one.
	// When false (or when the flavor group lookup fails), falls back to using the VM's
	// own reported resources (memory + vcpus).
	// Default: false
	UseFlavorGroupResources bool `json:"useFlavorGroupResources"`
}

// intPtr returns a pointer to the given int value.
func intPtr(i int) *int {
	return &i
}

// ApplyDefaults fills in any unset values with defaults.
func (c *FailoverConfig) ApplyDefaults() {
	defaults := DefaultConfig()
	if c.DatasourceName == "" {
		c.DatasourceName = defaults.DatasourceName
	}
	if c.SchedulerURL == "" {
		c.SchedulerURL = defaults.SchedulerURL
	}
	if c.ReconcileInterval.Duration == 0 {
		c.ReconcileInterval = defaults.ReconcileInterval
	}
	if c.Creator == "" {
		c.Creator = defaults.Creator
	}
	if c.FlavorFailoverRequirements == nil {
		c.FlavorFailoverRequirements = defaults.FlavorFailoverRequirements
	}
	if c.RevalidationInterval.Duration == 0 {
		c.RevalidationInterval = defaults.RevalidationInterval
	}
	if c.ShortReconcileInterval.Duration == 0 {
		c.ShortReconcileInterval = defaults.ShortReconcileInterval
	}
	if c.MinSuccessForShortInterval == nil {
		c.MinSuccessForShortInterval = defaults.MinSuccessForShortInterval
	}
	if c.MaxFailuresForShortInterval == nil {
		c.MaxFailuresForShortInterval = defaults.MaxFailuresForShortInterval
	}
	if c.MaxVMsToProcess == 0 {
		c.MaxVMsToProcess = defaults.MaxVMsToProcess
	}
	if c.VMSelectionRotationInterval == nil {
		c.VMSelectionRotationInterval = defaults.VMSelectionRotationInterval
	}
}

// DefaultConfig returns a default configuration.
func DefaultConfig() FailoverConfig {
	return FailoverConfig{
		FlavorFailoverRequirements:          map[string]int{"*": 2}, // by default all VMs get 2 failover reservations
		ReconcileInterval:                   metav1.Duration{Duration: 30 * time.Second},
		ShortReconcileInterval:              metav1.Duration{Duration: 100 * time.Millisecond},
		MinSuccessForShortInterval:          intPtr(1),
		MaxFailuresForShortInterval:         intPtr(99),
		MaxVMsToProcess:                     30,
		Creator:                             "cortex-failover-controller",
		DatasourceName:                      "nova-servers", // we have the server and flavor data source (both store in same postgres and same secret but still)
		SchedulerURL:                        "http://localhost:8080/scheduler/nova/external",
		TrustHypervisorLocation:             false,
		RevalidationInterval:                metav1.Duration{Duration: 30 * time.Minute},
		LimitOneNewReservationPerHypervisor: true,
		VMSelectionRotationInterval:         intPtr(4),
	}
}
