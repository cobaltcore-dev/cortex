// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package monitoring

import (
	"github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	dto "github.com/prometheus/client_model/go"
)

// Custom prometheus registry that adds functionality to the default registry.
type Registry struct {
	// Inherited prometheus registry.
	*prometheus.Registry
	// Custom configuration for the monitoring.
	config conf.MonitoringConfig
}

// Create a new registry with the given configuration.
// This registry will include the default go collector and process collector.
func NewRegistry(config conf.MonitoringConfig) *Registry {
	registry := &Registry{
		Registry: prometheus.NewRegistry(),
		config:   config,
	}
	// Add go execution stats and process metrics to the registry.
	registry.MustRegister(collectors.NewGoCollector())
	registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return registry
}

// Custom gather method that adds custom labels to all metrics.
func (r *Registry) Gather() ([]*dto.MetricFamily, error) {
	families, err := r.Registry.Gather()
	if err != nil {
		return nil, err
	}
	// Add a custom label to all metrics. This is useful for distinguishing
	// the metrics from other golang services that also use the default
	// go collector metrics.
	for name, value := range r.config.Labels {
		for _, family := range families {
			for _, metric := range family.Metric {
				metric.Label = append(metric.Label, &dto.LabelPair{
					Name:  &name,
					Value: &value,
				})
			}
		}
	}
	return families, nil
}
