// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type NovaFilter = lib.Filter[compute.SchedulerRequest]

// Configuration of filters supported by the nova scheduler.
var Index = map[string]func() NovaFilter{}
