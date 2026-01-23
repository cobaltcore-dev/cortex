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
type WeigherValidator[RequestType PipelineRequest] struct {
	// The wrapped weigher to validate.
	Weigher Step[RequestType]
}

// Initialize the wrapped weigher with the database and options.
func (s *WeigherValidator[RequestType]) Init(ctx context.Context, client client.Client, step v1alpha1.StepSpec) error {
	slog.Info("scheduler: init validation for step", "name", step.Name)
	return s.Weigher.Init(ctx, client, step)
}

// Validate the wrapped weigher with the database and options.
func validateWeigher[RequestType PipelineRequest](weigher Step[RequestType]) *WeigherValidator[RequestType] {
	return &WeigherValidator[RequestType]{Weigher: weigher}
}

// Run the weigher and validate what happens.
func (s *WeigherValidator[RequestType]) Run(traceLog *slog.Logger, request RequestType) (*StepResult, error) {
	result, err := s.Weigher.Run(traceLog, request)
	if err != nil {
		return nil, err
	}
	// Note that for some schedulers the same subject (e.g. compute host) may
	// appear multiple times if there is a substruct (e.g. hypervisor hostname).
	// Since cortex will only schedule on the subject level and not below,
	// we need to deduplicate the subjects first before the validation.
	deduplicated := map[string]struct{}{}
	for _, subject := range request.GetSubjects() {
		deduplicated[subject] = struct{}{}
	}
	if len(result.Activations) != len(deduplicated) {
		return nil, errors.New("safety: number of (deduplicated) subjects changed during step execution")
	}
	// Validate that some subjects remain.
	if len(result.Activations) == 0 {
		return nil, errors.New("safety: no subjects remain after step execution")
	}
	return result, nil
}
