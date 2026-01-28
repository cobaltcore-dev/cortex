// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	api "github.com/cobaltcore-dev/cortex/api/delegation/cinder"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type CinderFilter = lib.Filter[api.ExternalSchedulerRequest]

// Configuration of filters supported by the cinder scheduling.
var Index = map[string]func() CinderFilter{}
