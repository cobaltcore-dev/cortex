// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"github.com/cobaltcore-dev/cortex/api/external/pods"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type PodFilter = lib.Filter[pods.PodPipelineRequest]

// Configuration of filters supported by the pods scheduler.
var Index = map[string]func() PodFilter{}
