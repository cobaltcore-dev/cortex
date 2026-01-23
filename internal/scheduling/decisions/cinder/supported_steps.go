// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	api "github.com/cobaltcore-dev/cortex/api/delegation/cinder"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type CinderWeigher = lib.Step[api.ExternalSchedulerRequest]

// Configuration of weighers supported by the cinder scheduling.
var supportedWeighers = map[string]func() CinderWeigher{}

type CinderFilter = lib.Step[api.ExternalSchedulerRequest]

// Configuration of filters supported by the cinder scheduling.
var supportedFilters = map[string]func() CinderFilter{}
