// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"github.com/cobaltcore-dev/cortex/api/external/ironcore"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type MachineWeigher = lib.Weigher[ironcore.MachinePipelineRequest]

// Configuration of weighers supported by the machine scheduling.
var Index = map[string]func() MachineWeigher{}
