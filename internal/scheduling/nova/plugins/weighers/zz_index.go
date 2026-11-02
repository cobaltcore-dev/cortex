// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type NovaWeigher = lib.Weigher[compute.SchedulerRequest]

// Configuration of weighers supported by the nova scheduler.
var Index = map[string]func() NovaWeigher{}
