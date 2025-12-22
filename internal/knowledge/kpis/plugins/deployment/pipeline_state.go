// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package deployment

import (
	"context"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type PipelineStateKPIOpts struct {
	// The operator to filter pipelines by.
	PipelineOperator string `yaml:"pipelineOperator"`
}

// KPI observing the state of pipeline resources managed by cortex.
type PipelineStateKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[PipelineStateKPIOpts]

	// Prometheus descriptor for the pipeline state metric.
	counter *prometheus.Desc
}

func (PipelineStateKPI) GetName() string { return "pipeline_state_kpi" }

// Initialize the KPI.
func (k *PipelineStateKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.counter = prometheus.NewDesc(
		"cortex_pipeline_state",
		"State of cortex managed pipelines",
		[]string{"operator", "pipeline", "state"},
		nil,
	)
	return nil
}

// Conform to the prometheus collector interface by providing the descriptor.
func (k *PipelineStateKPI) Describe(ch chan<- *prometheus.Desc) { ch <- k.counter }

// Collect the pipeline state metrics.
func (k *PipelineStateKPI) Collect(ch chan<- prometheus.Metric) {
	// Get all pipelines with the specified pipeline operator.
	pipelineList := &v1alpha1.PipelineList{}
	if err := k.Client.List(context.Background(), pipelineList); err != nil {
		return
	}
	var pipelines []v1alpha1.Pipeline
	for _, pipeline := range pipelineList.Items {
		if pipeline.Spec.Operator != k.Options.PipelineOperator {
			continue
		}
		pipelines = append(pipelines, pipeline)
	}
	// For each pipeline, emit a metric with its state.
	for _, pipeline := range pipelines {
		var state string
		switch {
		case meta.IsStatusConditionTrue(pipeline.Status.Conditions, v1alpha1.PipelineConditionError):
			state = "error"
		case pipeline.Status.Ready:
			state = "ready"
		default:
			state = "unknown"
		}
		ch <- prometheus.MustNewConstMetric(
			k.counter, prometheus.GaugeValue, 1,
			k.Options.PipelineOperator, pipeline.Name, state,
		)
	}
}
