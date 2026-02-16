// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	api "github.com/cobaltcore-dev/cortex/api/external/manila"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type ManilaWeigher = lib.Weigher[api.ExternalSchedulerRequest]

// Configuration of weighers supported by the manila scheduler.
var Index = map[string]func() ManilaWeigher{}
