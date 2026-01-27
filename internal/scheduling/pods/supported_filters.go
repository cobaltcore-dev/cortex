// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package pods

import (
	"github.com/cobaltcore-dev/cortex/api/delegation/pods"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/pods/plugins/filters"
)

type PodFilter = lib.Filter[pods.PodPipelineRequest]

// Configuration of filters supported by the pods scheduler.
var supportedFilters = map[string]func() PodFilter{
	"noop":         func() PodFilter { return &filters.NoopFilter{} },
	"taint":        func() PodFilter { return &filters.TaintFilter{} },
	"nodeaffinity": func() PodFilter { return &filters.NodeAffinityFilter{} },
	"nodecapacity": func() PodFilter { return &filters.NodeCapacityFilter{} },
}
