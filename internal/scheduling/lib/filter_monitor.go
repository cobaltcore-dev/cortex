// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Wraps a scheduler filter to monitor its execution.
type FilterMonitor[RequestType PipelineRequest] struct {
	// The filter to monitor.
	filter Filter[RequestType]
	// The monitor tracking the step's execution.
	monitor *StepMonitor[RequestType]
}

// Wrap the given filter with a monitor.
func monitorFilter[RequestType PipelineRequest](
	filter Filter[RequestType],
	stepName string,
	m PipelineMonitor,
) *FilterMonitor[RequestType] {

	return &FilterMonitor[RequestType]{
		filter:  filter,
		monitor: monitorStep[RequestType](stepName, m),
	}
}

// Initialize the wrapped filter.
func (fm *FilterMonitor[RequestType]) Init(ctx context.Context, client client.Client, step v1alpha1.FilterSpec) error {
	return fm.filter.Init(ctx, client, step)
}

// Run the filter and observe its execution.
func (fm *FilterMonitor[RequestType]) Run(traceLog *slog.Logger, request RequestType) (*StepResult, error) {
	return fm.monitor.RunWrapped(traceLog, request, fm.filter)
}
