// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	api "github.com/cobaltcore-dev/cortex/api/delegation/cinder"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type CinderWeigher = lib.Weigher[api.ExternalSchedulerRequest]

// Configuration of weighers supported by the cinder scheduling.
var supportedWeighers = map[string]func() CinderWeigher{}
