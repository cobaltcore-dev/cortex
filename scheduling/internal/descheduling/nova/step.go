// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"

	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/descheduling/nova/plugins"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	// This error is returned from the step at any time when the step should be skipped.
	ErrStepSkipped = errors.New("step skipped")
)

type Step interface {
	// Get the VMs on their current hosts that should be considered for descheduling.
	Run() ([]plugins.Decision, error)
	// Configure the step with a database and options.
	Init(ctx context.Context, client client.Client, step v1alpha1.Step) error
	// Deinitialize the step, cleaning up any resources if needed.
	Deinit(ctx context.Context) error
}
