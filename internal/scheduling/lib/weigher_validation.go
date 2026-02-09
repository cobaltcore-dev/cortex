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
type WeigherValidator[RequestType FilterWeigherPipelineRequest] struct {
	// The wrapped weigher to validate.
	Weigher Weigher[RequestType]
}

// Initialize the wrapped weigher with the database and options.
func (s *WeigherValidator[RequestType]) Init(ctx context.Context, client client.Client, step v1alpha1.WeigherSpec) error {
	slog.Info("scheduler: init validation for step", "name", step.Name)
	return s.Weigher.Init(ctx, client, step)
}

// Validate the wrapped weigher with the database and options.
func validateWeigher[RequestType FilterWeigherPipelineRequest](weigher Weigher[RequestType]) *WeigherValidator[RequestType] {
	return &WeigherValidator[RequestType]{Weigher: weigher}
}

// Run the weigher and validate what happens.
func (s *WeigherValidator[RequestType]) Run(traceLog *slog.Logger, request RequestType) (*FilterWeigherPipelineStepResult, error) {
	result, err := s.Weigher.Run(traceLog, request)
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
	if len(result.Activations) != len(deduplicated) {
		return nil, errors.New("safety: number of (deduplicated) hosts changed during step execution")
	}
	// Validate that some hosts remain.
	if len(result.Activations) == 0 {
		return nil, errors.New("safety: no hosts remain after step execution")
	}
	return result, nil
}
