// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package machines

import (
	"github.com/cobaltcore-dev/cortex/api/delegation/ironcore"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type MachineStep = lib.Step[ironcore.MachinePipelineRequest]

// Configuration of steps supported by the scheduling.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = map[string]func() MachineStep{
	"noop": func() MachineStep { return &NoopFilter{} },
}
