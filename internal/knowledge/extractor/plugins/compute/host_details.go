// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"context"
	_ "embed"
	"errors"
	"strings"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type HostDetails struct {
	// Name of the OpenStack compute host.
	ComputeHost string `db:"compute_host"`
	// Availability zone of the compute host.
	AvailabilityZone string `db:"availability_zone"`
	// CPU Architecture of the compute host.
	// Can be "cascade-lake" or "sapphire-rapids"
	CPUArchitecture string `db:"cpu_architecture"`
	// Hypervisor type of the compute host.
	HypervisorType string `db:"hypervisor_type"`
	// Hypervisor family of the compute host.
	// Can be "kvm" or "vmware"
	HypervisorFamily string `db:"hypervisor_family"`
	// Amount of VMs currently running on the compute host.
	RunningVMs int `db:"running_vms"`
	// Type of workload running on the compute host.
	// Can be "general-purpose" or "hana"
	WorkloadType string `db:"workload_type"`
	// Whether the compute host is decommissioned.
	Decommissioned bool `db:"decommissioned"`
	// Whether the compute host is reserved for external customers.
	ExternalCustomer bool `db:"external_customer"`
	// Whether the compute host can be used for workloads.
	Enabled bool `db:"enabled"`
	// Reason why the compute host is disabled, if applicable.
	DisabledReason *string `db:"disabled_reason"`
	// Comma separated list of pinned projects of the ComputeHost.
	PinnedProjects *string `db:"pinned_projects"`
}

type HostDetailsExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},    // No options passed through yaml config
		HostDetails, // Feature model
	]
}

//go:embed host_details.sql
var hostDetailsQuery string

// Extract the traits of a compute host from the database.
func (e *HostDetailsExtractor) Extract() ([]plugins.Feature, error) {
	if e.DB == nil {
		return nil, errors.New("database connection is not initialized")
	}
	var hostDetails []HostDetails
	if _, err := e.DB.Select(&hostDetails, hostDetailsQuery); err != nil {
		return nil, err
	}

	// Add the pinned projects to the host details.
	pinnedProjectsKnowledge := &v1alpha1.Knowledge{}
	if err := e.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "host-pinned-projects"},
		pinnedProjectsKnowledge,
	); err != nil {
		return nil, err
	}
	pinnedProjects, err := v1alpha1.
		UnboxFeatureList[HostPinnedProjects](pinnedProjectsKnowledge.Status.Raw)
	if err != nil {
		return nil, err
	}
	pinnedProjectsByComputeHost := make(map[string][]string)
	for _, pp := range pinnedProjects {
		if pp.ComputeHost != nil && pp.Label != nil {
			pinnedProjectsByComputeHost[*pp.ComputeHost] = append(
				pinnedProjectsByComputeHost[*pp.ComputeHost],
				*pp.Label,
			)
		}
	}
	for i, hd := range hostDetails {
		pps, ok := pinnedProjectsByComputeHost[hd.ComputeHost]
		if !ok {
			// No pinned projects for this host.
			continue
		}
		joined := strings.Join(pps, ",")
		hostDetails[i].PinnedProjects = &joined
	}

	// Add the availability zones to the host details.
	azKnowledge := &v1alpha1.Knowledge{}
	if err := e.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "host-az"},
		azKnowledge,
	); err != nil {
		return nil, err
	}
	hostAZs, err := v1alpha1.
		UnboxFeatureList[HostAZ](azKnowledge.Status.Raw)
	if err != nil {
		return nil, err
	}
	azByComputeHost := make(map[string]string)
	for _, hostAZ := range hostAZs {
		if hostAZ.AvailabilityZone == nil {
			continue
		}
		azByComputeHost[hostAZ.ComputeHost] = *hostAZ.AvailabilityZone
	}
	for i, hd := range hostDetails {
		az, ok := azByComputeHost[hd.ComputeHost]
		if !ok {
			hostDetails[i].AvailabilityZone = "unknown"
			continue
		}
		hostDetails[i].AvailabilityZone = az
	}
	return e.Extracted(hostDetails)
}
