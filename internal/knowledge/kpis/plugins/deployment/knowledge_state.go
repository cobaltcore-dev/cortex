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

type KnowledgeStateKPIOpts struct {
	// The operator to filter knowledges by.
	KnowledgeOperator string `yaml:"knowledgeOperator"`
}

// KPI observing the state of knowledge resources managed by cortex.
type KnowledgeStateKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[KnowledgeStateKPIOpts]

	// Prometheus descriptor for the knowledge state metric.
	counter *prometheus.Desc
}

func (KnowledgeStateKPI) GetName() string { return "knowledge_state_kpi" }

// Initialize the KPI.
func (k *KnowledgeStateKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.counter = prometheus.NewDesc(
		"cortex_knowledge_state",
		"State of cortex managed knowledges",
		[]string{"operator", "knowledge", "state"},
		nil,
	)
	return nil
}

// Conform to the prometheus collector interface by providing the descriptor.
func (k *KnowledgeStateKPI) Describe(ch chan<- *prometheus.Desc) { ch <- k.counter }

// Collect the knowledge state metrics.
func (k *KnowledgeStateKPI) Collect(ch chan<- prometheus.Metric) {
	// Get all knowledges with the specified knowledge operator.
	knowledgeList := &v1alpha1.KnowledgeList{}
	if err := k.Client.List(context.Background(), knowledgeList); err != nil {
		return
	}
	var knowledges []v1alpha1.Knowledge
	for _, kn := range knowledgeList.Items {
		if kn.Spec.Operator != k.Options.KnowledgeOperator {
			continue
		}
		knowledges = append(knowledges, kn)
	}
	// For each knowledge, emit a metric with its state.
	for _, kn := range knowledges {
		var state string
		switch {
		case meta.IsStatusConditionTrue(kn.Status.Conditions, v1alpha1.KnowledgeConditionError):
			state = "error"
		case kn.Status.IsReady():
			state = "ready"
		default:
			state = "unknown"
		}
		ch <- prometheus.MustNewConstMetric(
			k.counter, prometheus.GaugeValue, 1,
			k.Options.KnowledgeOperator, kn.Name, state,
		)
	}
}
