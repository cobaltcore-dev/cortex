// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	api "github.com/cobaltcore-dev/cortex/api/delegation/cinder"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type CinderStep = lib.Step[api.ExternalSchedulerRequest]

// Configuration of steps supported by the scheduler.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = map[string]func() CinderStep{}
