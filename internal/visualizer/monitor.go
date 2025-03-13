// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package visualizer

import (
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
)

// Collection of Prometheus metrics to monitor the visualizer server.
type Monitor struct {
	// Currently, no metrics are monitored for the visualizer module.
}

// Create a new visualizer monitor and register the necessary Prometheus metrics.
func NewVisualizerMonitor(registry *monitoring.Registry) Monitor {
	return Monitor{}
}
