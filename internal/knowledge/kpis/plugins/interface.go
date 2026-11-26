// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"context"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Each kpi plugin must conform to this interface.
type KPI interface {
	// Configure the kpi with its inputs, a kubernetes client, options,
	// and the registry to publish metrics on.
	Init(inputs Inputs, client client.Client, opts conf.RawOpts) error
	// Collect the kpi from the given data.
	Collect(ch chan<- prometheus.Metric)
	// Describe this metric.
	Describe(ch chan<- *prometheus.Desc)
	// Get the name of this kpi.
	// This name is used to identify the kpi in metrics, config, logs, etc.
	// Should be something like: "my_cool_kpi".
	GetName() string
}

// Inputs resolves the objects of the inputs configured for a kpi.
type Inputs interface {
	// Resolve fills the fields of target, a pointer to a struct, tagged with
	// `input:"<var>"` with the objects of the configured input with that var.
	Resolve(ctx context.Context, target any) error
}
