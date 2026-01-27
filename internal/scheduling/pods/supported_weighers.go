// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package pods

import (
	"github.com/cobaltcore-dev/cortex/api/delegation/pods"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/pods/plugins/weighers"
)

type PodWeigher = lib.Weigher[pods.PodPipelineRequest]

// Configuration of weighers supported by the pods scheduler.
var supportedWeighers = map[string]func() PodWeigher{
	"binpack": func() PodWeigher { return &weighers.BinpackingStep{} },
}
