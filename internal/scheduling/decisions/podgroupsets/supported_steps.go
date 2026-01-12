// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package podgroupsets

import (
	"github.com/cobaltcore-dev/cortex/api/delegation/podgroupsets"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type PodGroupSetStep = lib.Step[podgroupsets.PodGroupSetPipelineRequest]

// Configuration of steps supported by the scheduling.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = map[string]func() PodGroupSetStep{
	"noop": func() PodGroupSetStep { return &NoopFilter{} },
}
