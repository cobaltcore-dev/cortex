// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import (
	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
)

// The reservation controllers build the typed compute.SchedulerRequest directly
// (not via the Nova wire adapter), yet their flavors still carry Nova's extra-spec
// string conventions. This thin wrapper delegates to the shared parsers in
// api/external/nova so reservation scheduling honors the same capability
// constraints as regular placement, with the conventions understood in one place.

// HypervisorTypeCapability returns the required (key=value) hypervisor capabilities
// for a reservation scheduler request, so filter_capabilities keeps a workload off
// incompatible hosts. It extracts ALL of the flavor's "capabilities:<key>" extra
// specs (via api.SplitCapabilities, which canonicalizes the hypervisor_type casing
// and drops everything on an unsupported operator) — not just hypervisor_type.
//
// When the flavor names no hypervisor type and defaultQEMU is set (the failover
// controller seeds from running VMs whose flavor may omit it), the canonical "QEMU"
// hypervisor_type is added. Without defaultQEMU, a missing hypervisor type adds no
// hypervisor_type constraint.
func HypervisorTypeCapability(extraSpecs map[string]string, defaultQEMU bool) []compute.Capability {
	caps := api.SplitCapabilities(extraSpecs)
	if extraSpecs["capabilities:hypervisor_type"] == "" && defaultQEMU {
		caps = append(caps, compute.Capability{Name: "hypervisor_type", Value: "QEMU"})
	}
	return caps
}
