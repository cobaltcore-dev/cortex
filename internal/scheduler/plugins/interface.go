// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/api"
)

// Interface for a scheduler step.
type Step interface {
	// Configure the step with a database and options.
	Init(db db.DB, opts conf.RawOpts) error
	// Run this step of the scheduling pipeline.
	// Return a map of hostnames to activation values. Important: hosts that are
	// not in the map are considered as filtered out.
	// Provide a traceLog that contains the global request id and should
	// be used to log the step's execution.
	Run(traceLog *slog.Logger, request api.Request) (map[string]float64, error)
	// Get the name of this step.
	// The name is used to identify the step in metrics, config, logs, and more.
	// Should be something like: "my_cool_scheduler_step".
	GetName() string
}
