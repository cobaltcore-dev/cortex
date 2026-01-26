// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Wraps a scheduler weigher to monitor its execution.
type WeigherMonitor[RequestType PipelineRequest] struct {
	// The weigher to monitor.
	weigher Weigher[RequestType]
	// The monitor tracking the step's execution.
	monitor *StepMonitor[RequestType]
}

// Wrap the given weigher with a monitor.
func monitorWeigher[RequestType PipelineRequest](
	weigher Weigher[RequestType],
	stepName string,
	m PipelineMonitor,
) *WeigherMonitor[RequestType] {

	return &WeigherMonitor[RequestType]{
		weigher: weigher,
		monitor: monitorStep[RequestType](stepName, m),
	}
}

// Initialize the wrapped weigher.
func (wm *WeigherMonitor[RequestType]) Init(ctx context.Context, client client.Client, step v1alpha1.WeigherSpec) error {
	return wm.weigher.Init(ctx, client, step)
}

// Run the weigher and observe its execution.
func (wm *WeigherMonitor[RequestType]) Run(traceLog *slog.Logger, request RequestType) (*StepResult, error) {
	return wm.monitor.RunWrapped(traceLog, request, wm.weigher)
}
