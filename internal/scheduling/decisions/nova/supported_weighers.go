// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/decisions/nova/plugins/weighers"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type NovaWeigher = lib.Weigher[api.ExternalSchedulerRequest]

// Configuration of weighers supported by the nova scheduler.
var supportedWeighers = map[string]func() NovaWeigher{
	"vmware_anti_affinity_noisy_projects":     func() NovaWeigher { return &weighers.VMwareAntiAffinityNoisyProjectsStep{} },
	"vmware_avoid_long_term_contended_hosts":  func() NovaWeigher { return &weighers.VMwareAvoidLongTermContendedHostsStep{} },
	"vmware_avoid_short_term_contended_hosts": func() NovaWeigher { return &weighers.VMwareAvoidShortTermContendedHostsStep{} },
	"vmware_hana_binpacking":                  func() NovaWeigher { return &weighers.VMwareHanaBinpackingStep{} },
	"vmware_general_purpose_balancing":        func() NovaWeigher { return &weighers.VMwareGeneralPurposeBalancingStep{} },
}
