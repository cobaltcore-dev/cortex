// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"github.com/cobaltcore-dev/cortex/api/delegation/pods"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type PodWeigher = lib.Weigher[pods.PodPipelineRequest]

// Configuration of weighers supported by the pods scheduler.
var Index = map[string]func() PodWeigher{}
