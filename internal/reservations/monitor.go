// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import (
	"context"
	"log/slog"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	v1alpha1 "github.com/cobaltcore-dev/cortex/internal/reservations/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Monitor for reservations metrics.
type Monitor struct {
	// Client for the kubernetes API.
	client.Client
	// Configuration for the monitor.
	Config conf.ReservationsConfig

	// Metrics
	numberOfReservations *prometheus.GaugeVec
	reservedResources    *prometheus.GaugeVec
}

// Initialize the metrics and bind them to the registry.
func (m *Monitor) Init(r *monitoring.Registry) {
	m.numberOfReservations = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cortex_reservations_number",
		Help: "Number of reservations.",
	}, []string{"status_phase", "status_error", "spec_kind"})
	m.reservedResources = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cortex_reservations_resources",
		Help: "Resources reserved by reservations.",
	}, []string{"status_phase", "status_error", "spec_kind", "allocation_kind", "resource", "host"})
	r.MustRegister(m)
}

// Describe the metrics for Prometheus.
func (m *Monitor) Describe(ch chan<- *prometheus.Desc) {
	m.numberOfReservations.Describe(ch)
	m.reservedResources.Describe(ch)
}

// Collect the metrics on-demand and send them to Prometheus.
func (m *Monitor) Collect(ch chan<- prometheus.Metric) {
	// Fetch all reservations from kubernetes.
	var reservations v1alpha1.ReservationList
	if err := m.Client.List(
		context.Background(),
		&reservations,
		client.InNamespace(m.Config.Namespace),
	); err != nil {
		slog.Error("failed to list reservations", "err", err)
		return
	}

	countByLabels := map[string]uint64{}
	for _, reservation := range reservations.Items {
		key := string(reservation.Status.Phase) +
			"," + reservation.Status.Error +
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
		switch reservation.Status.Allocation.Kind {
		case v1alpha1.ReservationStatusAllocationKindCompute:
			host = reservation.Status.Allocation.Compute.Host
		default:
			continue // Skip non-compute reservations.
		}
		key := string(reservation.Status.Phase) +
			"," + reservation.Status.Error +
			"," + string(reservation.Spec.Kind) +
			"," + string(reservation.Status.Allocation.Kind) +
			"," + host
		if _, ok := resourcesByLabels[key]; !ok {
			resourcesByLabels[key] = map[string]uint64{}
		}
		switch reservation.Spec.Kind {
		case v1alpha1.ReservationSpecKindInstance:
			// Instance reservations have resources defined in the instance spec.
			resourcesByLabels[key]["vcpus"] += reservation.Spec.Instance.VCPUs.
				AsDec().UnscaledBig().Uint64()
			resourcesByLabels[key]["memory_mb"] += reservation.Spec.Instance.Memory.
				AsDec().UnscaledBig().Uint64() / 1000000
			resourcesByLabels[key]["disk_gb"] += reservation.Spec.Instance.Disk.
				AsDec().UnscaledBig().Uint64() / 1000000000
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
}
