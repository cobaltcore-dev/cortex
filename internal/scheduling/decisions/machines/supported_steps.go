// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package machines

import (
	"github.com/cobaltcore-dev/cortex/api/delegation/ironcore"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type MachineStep = lib.Step[ironcore.MachinePipelineRequest]

// Configuration of weighers supported by the machine scheduling.
var supportedWeighers = map[string]func() MachineStep{
	"noop": func() MachineStep { return &NoopFilter{} },
}

// Configuration of filters supported by the machine scheduling.
var supportedFilters = map[string]func() MachineStep{}
