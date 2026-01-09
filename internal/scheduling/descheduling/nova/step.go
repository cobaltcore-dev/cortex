// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/descheduling/nova/plugins"
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
	Init(ctx context.Context, client client.Client, step v1alpha1.StepSpec) error
}
