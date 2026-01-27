// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	api "github.com/cobaltcore-dev/cortex/api/delegation/manila"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type ManilaFilter = lib.Filter[api.ExternalSchedulerRequest]

// Configuration of filters supported by the manila scheduler.
var supportedFilters = map[string]func() ManilaFilter{}
