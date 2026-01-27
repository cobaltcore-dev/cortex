// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package machines

import (
	"github.com/cobaltcore-dev/cortex/api/delegation/ironcore"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type MachineFilter = lib.Filter[ironcore.MachinePipelineRequest]

// Configuration of filters supported by the machine scheduling.
var supportedFilters = map[string]func() MachineFilter{
	"noop": func() MachineFilter { return &NoopFilter{} },
}
