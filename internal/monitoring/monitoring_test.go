// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package monitoring

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/prometheus/client_golang/prometheus"
)

func TestNewRegistry(t *testing.T) {
	config := conf.MonitoringConfig{
		Labels: map[string]string{
			"env": "test",
		},
	}
	registry := NewRegistry(config)

	if registry == nil {
		t.Fatalf("expected registry to be non-nil")
	}
	if registry.config.Labels["env"] != "test" {
		t.Fatalf("expected registry config label 'env' to be 'test', got %v", registry.config.Labels["env"])
	}
}

func TestRegistry_Gather(t *testing.T) {
	config := conf.MonitoringConfig{
		Labels: map[string]string{
			"env": "test",
		},
	}
	registry := NewRegistry(config)

	// Register a custom metric
	counter := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "test_counter",
		Help: "A test counter",
	})
	registry.MustRegister(counter)
	counter.Inc()

	// Gather metrics
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Check that the custom label is added to all metrics
	for _, family := range families {
		for _, metric := range family.Metric {
			found := false
			for _, label := range metric.Label {
				if *label.Name == "env" && *label.Value == "test" {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected custom label 'env' with value 'test' in metric, but not found")
			}
		}
	}
}
