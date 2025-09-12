// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"strings"

	"github.com/cobaltcore-dev/cortex/reservations/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	monitorLog = ctrl.Log.WithName("monitor")
)

// Monitor for reservations metrics.
type Monitor struct {
	// Client for the kubernetes API.
	client.Client

	// Metrics
	numberOfReservations *prometheus.GaugeVec
	reservedResources    *prometheus.GaugeVec
}

// Initialize the metrics and bind them to the registry.
func (m *Monitor) Init() {
	m.numberOfReservations = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cortex_reservations_number",
		Help: "Number of reservations.",
	}, []string{"status_phase", "status_error", "spec_kind"})
	m.reservedResources = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cortex_reservations_resources",
		Help: "Resources reserved by reservations.",
	}, []string{"status_phase", "status_error", "spec_kind", "host", "resource"})
}

// Describe the metrics for Prometheus.
func (m *Monitor) Describe(ch chan<- *prometheus.Desc) {
	m.numberOfReservations.Describe(ch)
	m.reservedResources.Describe(ch)
}

// Collect the metrics on-demand and send them to Prometheus.
func (m *Monitor) Collect(ch chan<- prometheus.Metric) {
	// Fetch all reservations from kubernetes.
	var reservations v1alpha1.ComputeReservationList
	if err := m.List(
		context.Background(),
		&reservations,
	); err != nil {
		monitorLog.Error(err, "failed to list reservations")
		return
	}

	countByLabels := map[string]uint64{}
	for _, reservation := range reservations.Items {
		key := string(reservation.Status.Phase) +
			"," + strings.ReplaceAll(reservation.Status.Error, ",", ";") +
			"," + string(reservation.Spec.Kind)
		countByLabels[key]++
	}
	for key, count := range countByLabels {
		labelValues := strings.Split(key, ",")
		m.numberOfReservations.WithLabelValues(labelValues...).Set(float64(count))
	}
	m.numberOfReservations.Collect(ch)

	resourcesByLabels := map[string]map[string]uint64{}
	for _, reservation := range reservations.Items {
		host := ""
		key := string(reservation.Status.Phase) +
			"," + strings.ReplaceAll(reservation.Status.Error, ",", ";") +
			"," + string(reservation.Spec.Kind) +
			"," + host
		if _, ok := resourcesByLabels[key]; !ok {
			resourcesByLabels[key] = map[string]uint64{}
		}
		switch reservation.Spec.Kind {
		case v1alpha1.ComputeReservationSpecKindInstance:
			// Instance reservations have resources defined in the instance spec.
			if cpu, ok := reservation.Spec.Instance.Requests["cpu"]; ok {
				resourcesByLabels[key]["vcpus"] += cpu.AsDec().UnscaledBig().Uint64()
			}
			if memory, ok := reservation.Spec.Instance.Requests["memory"]; ok {
				resourcesByLabels[key]["memory_mb"] += memory.AsDec().UnscaledBig().Uint64() / 1000000
			}
		default:
			continue // Skip non-instance reservations.
		}
	}
	for key, resources := range resourcesByLabels {
		labelValues := strings.Split(key, ",")
		for resource, value := range resources {
			m.reservedResources.
				WithLabelValues(append(labelValues, resource)...).
				Set(float64(value))
		}
	}
	m.reservedResources.Collect(ch)
	monitorLog.Info("collected reservation metrics", "reservations", len(reservations.Items))
}
