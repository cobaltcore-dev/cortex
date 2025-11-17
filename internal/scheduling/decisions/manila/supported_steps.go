// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	api "github.com/cobaltcore-dev/cortex/api/delegation/manila"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/decisions/manila/plugins/netapp"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type ManilaStep = lib.Step[api.ExternalSchedulerRequest]

// Configuration of steps supported by the scheduling.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = map[string]func() ManilaStep{
	"netapp_cpu_usage_balancing": func() ManilaStep { return &netapp.CPUUsageBalancingStep{} },
}
