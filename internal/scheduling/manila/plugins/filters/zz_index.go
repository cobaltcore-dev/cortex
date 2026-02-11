// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	api "github.com/cobaltcore-dev/cortex/api/external/manila"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type ManilaFilter = lib.Filter[api.ExternalSchedulerRequest]

// Configuration of filters supported by the manila scheduler.
var Index = map[string]func() ManilaFilter{}
