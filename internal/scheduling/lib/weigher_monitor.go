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
type WeigherMonitor[RequestType FilterWeigherPipelineRequest] struct {
	// The weigher to monitor.
	weigher Weigher[RequestType]
	// The monitor tracking the step's execution.
	monitor *FilterWeigherPipelineStepMonitor[RequestType]
}

// Wrap the given weigher with a monitor.
func monitorWeigher[RequestType FilterWeigherPipelineRequest](
	weigher Weigher[RequestType],
	stepName string,
	m FilterWeigherPipelineMonitor,
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
func (wm *WeigherMonitor[RequestType]) Run(ctx context.Context, traceLog *slog.Logger, request RequestType) (*FilterWeigherPipelineStepResult, error) {
	return wm.monitor.RunWrapped(ctx, traceLog, request, wm.weigher)
}
