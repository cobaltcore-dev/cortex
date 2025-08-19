// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package checks

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/commands/checks/cinder"
	"github.com/cobaltcore-dev/cortex/commands/checks/manila"
	"github.com/cobaltcore-dev/cortex/commands/checks/nova"
	"github.com/cobaltcore-dev/cortex/internal/conf"
)

var checks = map[string]func(context.Context, conf.Config){
	"nova":   nova.RunChecks,
	"manila": manila.RunChecks,
	"cinder": cinder.RunChecks,
}

// Run all checks.
func RunChecks(ctx context.Context, config conf.Config) {
	logSeparator := "----------------------------------------"
	for _, name := range config.GetChecks() {
		slog.Info(logSeparator)
		slog.Info("running check", "name", name)
		checks[name](ctx, config)
		slog.Info("check completed", "name", name)
		slog.Info(logSeparator)
	}
}
