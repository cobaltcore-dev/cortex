// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"context"
	"errors"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Common base for all KPIs that provides some functionality
// that would otherwise be duplicated across all KPIs.
type BaseKPI[Opts any] struct {
	// Options to pass via yaml to this step.
	conf.JsonOpts[Opts]
	// Inputs configured for the kpi.
	Inputs Inputs
	// Kubernetes client to access other resources.
	Client client.Client
}

// Init the KPI with its inputs, a kubernetes client, and options.
func (k *BaseKPI[Opts]) Init(inputs Inputs, client client.Client, opts conf.RawOpts) error {
	if err := k.Load(opts); err != nil {
		return err
	}
	k.Inputs = inputs
	k.Client = client
	return nil
}

// ResolveInputs fills the fields of target tagged with `input:"<var>"` with
// the objects of the inputs configured for the kpi.
func (k *BaseKPI[Opts]) ResolveInputs(ctx context.Context, target any) error {
	if k.Inputs == nil {
		return errors.New("no inputs configured")
	}
	return k.Inputs.Resolve(ctx, target)
}
