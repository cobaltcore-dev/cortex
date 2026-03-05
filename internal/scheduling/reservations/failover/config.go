// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import "time"

// FailoverConfig defines the configuration for failover reservation management.
type FailoverConfig struct {
	// FlavorFailoverRequirements maps flavor name patterns to required failover count.
	// Example: {"hana_*": 2, "m1.xlarge": 1}
	// A VM with a matching flavor will need this many failover reservations.
	FlavorFailoverRequirements map[string]int `json:"flavorFailoverRequirements"`

	// ReconcileInterval is how often to check for missing failover reservations.
	ReconcileInterval time.Duration `json:"reconcileInterval"`

	// Creator tag for failover reservations (for identification and cleanup).
	Creator string `json:"creator"`

	// DatasourceName is the name of the Datasource CRD that provides database connection info.
	// This is used to read VM data from the Nova database.
	DatasourceName string `json:"datasourceName"`

	// SchedulerURL is the URL of the nova external scheduler API.
	// Example: "http://localhost:8080/scheduler/nova/external"
	SchedulerURL string `json:"schedulerURL"`

	// MaxVMsToProcess limits the number of VMs to process per reconciliation cycle.
	// Set to 0 or negative to process all VMs (default behavior).
	// Useful for debugging and testing with large VM counts.
	MaxVMsToProcess int `json:"maxVMsToProcess"`
}

// DefaultConfig returns a default configuration.
func DefaultConfig() FailoverConfig {
	return FailoverConfig{
		FlavorFailoverRequirements: map[string]int{"*": 2}, // by default general purpose 1 and hana 2 failover reservations
		ReconcileInterval:          1 * time.Minute,
		Creator:                    "cortex-failover-controller",
		DatasourceName:             "nova-servers", // we have the server and flavor data source (both store in same postgres and same secret but still)
		SchedulerURL:               "http://localhost:8080/scheduler/nova/external",
	}
}
