// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"strings"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func NewControllerMonitor(k8sClient client.Client) Monitor {
	return Monitor{
		Client: k8sClient,
		numberOfReservations: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_reservations_number",
			Help: "Number of reservations.",
		}, []string{"status_phase", "status_error"}),
		reservedResources: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_reservations_resources",
			Help: "Resources reserved by reservations.",
		}, []string{"status_phase", "status_error", "host", "resource"}),
	}
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
	if err := m.List(
		context.Background(),
		&reservations,
	); err != nil {
		monitorLog.Error(err, "failed to list reservations")
		return
	}

	countByLabels := map[string]uint64{}
	for _, reservation := range reservations.Items {
		errorCondition := meta.FindStatusCondition(reservation.Status.Conditions, v1alpha1.ReservationConditionError)
		errorMsg := ""
		if errorCondition != nil && errorCondition.Status == metav1.ConditionTrue {
			errorMsg = errorCondition.Message
		}
		key := string(reservation.Status.Phase) +
			"," + strings.ReplaceAll(errorMsg, ",", ";")
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
		errorCondition := meta.FindStatusCondition(reservation.Status.Conditions, v1alpha1.ReservationConditionError)
		errorMsg := ""
		if errorCondition != nil && errorCondition.Status == metav1.ConditionTrue {
			errorMsg = errorCondition.Message
		}
		key := string(reservation.Status.Phase) +
			"," + strings.ReplaceAll(errorMsg, ",", ";") +
			"," + host
		if _, ok := resourcesByLabels[key]; !ok {
			resourcesByLabels[key] = map[string]uint64{}
		}
		for resourceName, resourceQuantity := range reservation.Spec.Resources {
			resourcesByLabels[key][resourceName] += resourceQuantity.AsDec().UnscaledBig().Uint64()
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
