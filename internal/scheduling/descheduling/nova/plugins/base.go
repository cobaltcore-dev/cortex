// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"context"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Common base for all descheduler steps that provides some functionality
// that would otherwise be duplicated across all steps.
type Detector[Opts any] struct {
	// Options to pass via yaml to this step.
	conf.JsonOpts[Opts]
	// The kubernetes client to use.
	Client client.Client
}

// Init the step with the database and options.
func (s *Detector[Opts]) Init(ctx context.Context, client client.Client, step v1alpha1.StepSpec) error {
	opts := conf.NewRawOptsBytes(step.Opts.Raw)
	if err := s.Load(opts); err != nil {
		return err
	}

	s.Client = client
	return nil
}

type Decision struct {
	// Get the VM ID for which this decision applies.
	VMID string
	// Get a human-readable reason for this decision.
	Reason string
	// Get the compute host where the vm should be migrated away from.
	Host string
}
