// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package pods

import (
	"github.com/cobaltcore-dev/cortex/api/delegation/pods"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/decisions/pods/plugins/filters"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type PodStep = lib.Step[pods.PodPipelineRequest]

// Configuration of steps supported by the scheduling.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = map[string]func() PodStep{
	"noop":         func() PodStep { return &filters.NoopFilter{} },
	"taint":        func() PodStep { return &filters.TaintFilter{} },
	"nodeaffinity": func() PodStep { return &filters.NodeAffinityFilter{} },
	"nodecapacity": func() PodStep { return &filters.NodeCapacityFilter{} },
}
