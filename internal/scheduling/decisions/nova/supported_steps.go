// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/decisions/nova/plugins/filters"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/decisions/nova/plugins/weighers"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type NovaStep = lib.Step[api.ExternalSchedulerRequest]

// Configuration of steps supported by the scheduling.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = map[string]func() NovaStep{
	"vmware_anti_affinity_noisy_projects":     func() NovaStep { return &weighers.VMwareAntiAffinityNoisyProjectsStep{} },
	"vmware_avoid_long_term_contended_hosts":  func() NovaStep { return &weighers.VMwareAvoidLongTermContendedHostsStep{} },
	"vmware_avoid_short_term_contended_hosts": func() NovaStep { return &weighers.VMwareAvoidShortTermContendedHostsStep{} },
	"vmware_hana_binpacking":                  func() NovaStep { return &weighers.VMwareHanaBinpackingStep{} },
	"vmware_general_purpose_balancing":        func() NovaStep { return &weighers.VMwareGeneralPurposeBalancingStep{} },
	"filter_has_accelerators":                 func() NovaStep { return &filters.FilterHasAcceleratorsStep{} },
	"filter_correct_az":                       func() NovaStep { return &filters.FilterCorrectAZStep{} },
	"filter_status_conditions":                func() NovaStep { return &filters.FilterStatusConditionsStep{} },
	"filter_maintenance":                      func() NovaStep { return &filters.FilterMaintenanceStep{} },
	"filter_packed_virtqueue":                 func() NovaStep { return &filters.FilterPackedVirtqueueStep{} },
	"filter_external_customer":                func() NovaStep { return &filters.FilterExternalCustomerStep{} },
	"filter_allowed_projects":                 func() NovaStep { return &filters.FilterAllowedProjectsStep{} },
	"filter_capabilities":                     func() NovaStep { return &filters.FilterCapabilitiesStep{} },
	"filter_has_requested_traits":             func() NovaStep { return &filters.FilterHasRequestedTraits{} },
	"filter_has_enough_capacity":              func() NovaStep { return &filters.FilterHasEnoughCapacity{} },
	"filter_host_instructions":                func() NovaStep { return &filters.FilterHostInstructionsStep{} },
	"filter_instance_group_affinity":          func() NovaStep { return &filters.FilterInstanceGroupAffinityStep{} },
	"filter_instance_group_anti_affinity":     func() NovaStep { return &filters.FilterInstanceGroupAntiAffinityStep{} },
}
