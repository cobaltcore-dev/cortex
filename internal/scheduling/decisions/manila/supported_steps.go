// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	api "github.com/cobaltcore-dev/cortex/api/delegation/manila"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/decisions/manila/plugins/weighers"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type ManilaFilter = lib.Step[api.ExternalSchedulerRequest]

// Configuration of filters supported by the manila scheduler.
var supportedFilters = map[string]func() ManilaFilter{}

type ManilaWeigher = lib.Step[api.ExternalSchedulerRequest]

// Configuration of weighers supported by the manila scheduler.
var supportedWeighers = map[string]func() ManilaWeigher{
	"netapp_cpu_usage_balancing": func() ManilaWeigher { return &weighers.NetappCPUUsageBalancingStep{} },
}
