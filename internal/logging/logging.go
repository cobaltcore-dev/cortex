// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package logging

import "log/slog"

func Default() *slog.Logger {
	// This may include more logic in the future when we want to
	// customize the logging behavior.
	return slog.Default()
}

var Log = Default()
