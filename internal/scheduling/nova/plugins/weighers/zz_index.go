// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type NovaWeigher = lib.Weigher[api.ExternalSchedulerRequest]

// Configuration of weighers supported by the nova scheduler.
var Index = map[string]func() NovaWeigher{}
