// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/decisions/nova/plugins/filters"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type NovaFilter = lib.Filter[api.ExternalSchedulerRequest]

// Configuration of filters supported by the nova scheduler.
var supportedFilters = map[string]func() NovaFilter{
	"filter_has_accelerators":             func() NovaFilter { return &filters.FilterHasAcceleratorsStep{} },
	"filter_correct_az":                   func() NovaFilter { return &filters.FilterCorrectAZStep{} },
	"filter_status_conditions":            func() NovaFilter { return &filters.FilterStatusConditionsStep{} },
	"filter_maintenance":                  func() NovaFilter { return &filters.FilterMaintenanceStep{} },
	"filter_packed_virtqueue":             func() NovaFilter { return &filters.FilterPackedVirtqueueStep{} },
	"filter_external_customer":            func() NovaFilter { return &filters.FilterExternalCustomerStep{} },
	"filter_allowed_projects":             func() NovaFilter { return &filters.FilterAllowedProjectsStep{} },
	"filter_capabilities":                 func() NovaFilter { return &filters.FilterCapabilitiesStep{} },
	"filter_has_requested_traits":         func() NovaFilter { return &filters.FilterHasRequestedTraits{} },
	"filter_has_enough_capacity":          func() NovaFilter { return &filters.FilterHasEnoughCapacity{} },
	"filter_host_instructions":            func() NovaFilter { return &filters.FilterHostInstructionsStep{} },
	"filter_instance_group_affinity":      func() NovaFilter { return &filters.FilterInstanceGroupAffinityStep{} },
	"filter_instance_group_anti_affinity": func() NovaFilter { return &filters.FilterInstanceGroupAntiAffinityStep{} },
	"filter_live_migratable":              func() NovaFilter { return &filters.FilterLiveMigratableStep{} },
	"filter_requested_destination":        func() NovaFilter { return &filters.FilterRequestedDestinationStep{} },
}
