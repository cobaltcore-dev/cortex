// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type NovaFilter = lib.Filter[api.ExternalSchedulerRequest]

// Configuration of filters supported by the nova scheduler.
var Index = map[string]func() NovaFilter{}
