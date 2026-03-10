// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"errors"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Wrapper for scheduler steps that validates them before/after execution.
type FilterValidator[RequestType FilterWeigherPipelineRequest] struct {
	// The wrapped filter to validate.
	Filter Filter[RequestType]
}

// Initialize the wrapped filter with the database and options.
func (s *FilterValidator[RequestType]) Init(ctx context.Context, client client.Client, step v1alpha1.FilterSpec) error {
	slog.Info("scheduler: init validation for step", "name", step.Name)
	return s.Filter.Init(ctx, client, step)
}

// Validate the wrapped filter.
func (s *FilterValidator[RequestType]) Validate(ctx context.Context, params v1alpha1.Parameters) error {
	return s.Filter.Validate(ctx, params)
}

// Validate the wrapped filter with the database and options.
func validateFilter[RequestType FilterWeigherPipelineRequest](filter Filter[RequestType]) *FilterValidator[RequestType] {
	return &FilterValidator[RequestType]{Filter: filter}
}

// Run the filter and validate what happens.
func (s *FilterValidator[RequestType]) Run(traceLog *slog.Logger, request RequestType) (*FilterWeigherPipelineStepResult, error) {
	result, err := s.Filter.Run(traceLog, request)
	if err != nil {
		return nil, err
	}
	// Note that for some schedulers the same host (e.g. compute host) may
	// appear multiple times if there is a substruct (e.g. hypervisor hostname).
	// Since cortex will only schedule on the host level and not below,
	// we need to deduplicate the hosts first before the validation.
	deduplicated := map[string]struct{}{}
	for _, host := range request.GetHosts() {
		deduplicated[host] = struct{}{}
	}
	// Filters can only remove hosts, not add new ones.
	if len(result.Activations) > len(deduplicated) {
		return nil, errors.New("safety: number of hosts increased during step execution")
	}
	return result, nil
}
