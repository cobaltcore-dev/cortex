// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import (
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
)

// ExtractTraits splits a flavor's "trait:<name>" extra specs into required and
// forbidden trait names, mirroring the Nova spec adapter so reservation
// scheduling honors the same trait constraints as regular placement. The
// reservation controllers build the typed compute.SchedulerRequest directly
// (not via the Nova wire adapter), so they must populate RequiredTraits/
// ForbiddenTraits themselves — otherwise filter_has_requested_traits sees empty
// slices and becomes a no-op. It delegates to compute.SplitTraits so the trait
// convention lives in one place.
func ExtractTraits(extraSpecs map[string]string) (required, forbidden []string) {
	return compute.SplitTraits(extraSpecs)
}

// HypervisorTypeCapability returns the capabilities:hypervisor_type match (QEMU
// vs CH) for a reservation scheduler request, so filter_capabilities keeps a
// workload off an incompatible hypervisor. When the flavor names no hypervisor
// type, it returns "qemu" if defaultQEMU is set (the failover controller seeds
// from running VMs whose flavor may omit it) and nil otherwise.
func HypervisorTypeCapability(extraSpecs map[string]string, defaultQEMU bool) []compute.Capability {
	hvType := extraSpecs["capabilities:hypervisor_type"]
	if hvType == "" {
		if !defaultQEMU {
			return nil
		}
		hvType = "qemu"
	}
	return []compute.Capability{{Name: "hypervisor_type", Value: hvType}}
}
