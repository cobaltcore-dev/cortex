// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"context"

	"github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Common base for all steps that provides some functionality
// that would otherwise be duplicated across all steps.
type BaseStep[Opts any] struct {
	// Options to pass via yaml to this step.
	conf.JsonOpts[Opts]
	// The kubernetes client to use.
	Client client.Client
	// Initialized database connection, if configured through the step spec.
	DB *db.DB
}

// Init the step with the database and options.
func (s *BaseStep[Opts]) Init(ctx context.Context, client client.Client, step v1alpha1.Step) error {
	opts := conf.NewRawOptsBytes(step.Spec.Opts.Raw)
	if err := s.Load(opts); err != nil {
		return err
	}

	if step.Spec.DatabaseSecretRef != nil {
		authenticatedDB, err := db.Connector{Client: client}.
			FromSecretRef(ctx, *step.Spec.DatabaseSecretRef)
		if err != nil {
			return err
		}
		s.DB = authenticatedDB
	}

	s.Client = client
	return nil
}

// Deinitialize the step, freeing any held resources.
func (s *BaseStep[Opts]) Deinit(ctx context.Context) error {
	if s.DB != nil {
		s.DB.Close()
	}
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
