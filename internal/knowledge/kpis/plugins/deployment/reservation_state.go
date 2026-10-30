// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package deployment

import (
	"context"
	"strings"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var reservationStateKPILogger = ctrl.Log.WithName("reservation-state-kpi")

type ReservationStateKPIOpts struct {
	// The scheduling domain to filter reservations by.
	ReservationSchedulingDomain v1alpha1.SchedulingDomain `json:"reservationSchedulingDomain"`
}

// KPI observing the state of reservation resources managed by cortex.
// Metrics are labeled by the reservation type (e.g. InFlightReservation,
// CommittedResourceReservation, FailoverReservation) so that operators can
// alert on specific reservation kinds. The state label mirrors the
// v1alpha1.ReservationConditionReady condition status and the reason label
// carries the condition's Reason so that alerts can target specific failure
// modes (e.g. reason="InstanceNotFound" for in-flight reservations whose VM
// has not spawned on any hypervisor yet).
//
// All metrics are filtered to the configured ReservationSchedulingDomain.
type ReservationStateKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[ReservationStateKPIOpts]

	// Prometheus descriptor for the reservation state metric.
	counter *prometheus.Desc
	// Prometheus descriptor for the reservation count metric.
	reservationCount *prometheus.Desc
	// Prometheus descriptor for the reserved resources metric.
	allocatedResources *prometheus.Desc
}

func (ReservationStateKPI) GetName() string { return "reservation_state_kpi" }

// Initialize the KPI.
func (k *ReservationStateKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.counter = prometheus.NewDesc(
		"cortex_reservation_state",
		"State of cortex managed reservations",
		[]string{"domain", "type", "state", "reason"},
		nil,
	)
	k.reservationCount = prometheus.NewDesc(
		"cortex_reservation_count",
		"Number of reservations by type, readiness and error status.",
		[]string{"domain", "type", "status_ready", "status_error"},
		nil,
	)
	k.allocatedResources = prometheus.NewDesc(
		"cortex_reservation_allocated_resources",
		"Resource units allocated across reservations, by type, host and resource type.",
		[]string{"domain", "type", "status_ready", "status_error", "host", "resource"},
		nil,
	)
	return nil
}

// Conform to the prometheus collector interface by providing the descriptors.
func (k *ReservationStateKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.counter
	ch <- k.reservationCount
	ch <- k.allocatedResources
}

// Collect the reservation state metrics.
func (k *ReservationStateKPI) Collect(ch chan<- prometheus.Metric) {
	// Bound the list call so a slow API server can't hang the Prometheus
	// scrape indefinitely; if it fails we log so the disappearance of the
	// reservation metric is not silent.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Get all reservations. The scheduling domain filter is applied per item
	// since a Reservation is cluster-scoped and may cover multiple domains.
	reservationList := &v1alpha1.ReservationList{}
	if err := k.Client.List(ctx, reservationList); err != nil {
		reservationStateKPILogger.Error(err, "Failed to list reservations")
		return
	}
	domain := string(k.Options.ReservationSchedulingDomain)

	// Aggregate counts by (type, state, reason) for cortex_reservation_state, by
	// (type, ready, error) for cortex_reservation_count, and by (type, ready, error,
	// host, resource) for cortex_reservation_allocated_resources. Aggregating keeps
	// metric cardinality bounded regardless of how many reservations exist.
	type stateBucket struct {
		reservationType string
		state           string
		reason          string
	}
	type countBucket struct {
		reservationType string
		statusReady     string
		statusError     string
	}
	type resourceBucket struct {
		reservationType string
		statusReady     string
		statusError     string
		host            string
		resource        string
	}
	stateCounts := map[stateBucket]float64{}
	reservationCounts := map[countBucket]float64{}
	allocatedResources := map[resourceBucket]uint64{}
	for _, r := range reservationList.Items {
		if r.Spec.SchedulingDomain != k.Options.ReservationSchedulingDomain {
			continue
		}
		reservationType := string(r.Spec.Type)

		// Condition-derived labels. "state" mirrors the ready condition for
		// cortex_reservation_state; "statusReady"/"statusError" carry the raw
		// condition status and message for the ported count/resource metrics.
		state := "unknown"
		reason := ""
		statusReady := "Unknown"
		statusError := ""
		cond := meta.FindStatusCondition(r.Status.Conditions, v1alpha1.ReservationConditionReady)
		if cond != nil {
			reason = cond.Reason
			statusReady = string(cond.Status)
			switch cond.Status {
			case metav1.ConditionTrue:
				state = "ready"
			case metav1.ConditionFalse:
				state = "error"
				statusError = strings.ReplaceAll(cond.Message, ",", ";")
			default:
				state = "unknown"
			}
		}

		stateCounts[stateBucket{reservationType: reservationType, state: state, reason: reason}]++
		reservationCounts[countBucket{reservationType: reservationType, statusReady: statusReady, statusError: statusError}]++
		for resourceName, resourceQuantity := range r.Spec.Resources {
			allocatedResources[resourceBucket{
				reservationType: reservationType,
				statusReady:     statusReady,
				statusError:     statusError,
				host:            r.Status.Host,
				resource:        string(resourceName),
			}] += resourceQuantity.AsDec().UnscaledBig().Uint64()
		}
	}
	for b, v := range stateCounts {
		ch <- prometheus.MustNewConstMetric(
			k.counter, prometheus.GaugeValue, v,
			domain, b.reservationType, b.state, b.reason,
		)
	}
	for b, v := range reservationCounts {
		ch <- prometheus.MustNewConstMetric(
			k.reservationCount, prometheus.GaugeValue, v,
			domain, b.reservationType, b.statusReady, b.statusError,
		)
	}
	for b, v := range allocatedResources {
		ch <- prometheus.MustNewConstMetric(
			k.allocatedResources, prometheus.GaugeValue, float64(v),
			domain, b.reservationType, b.statusReady, b.statusError, b.host, b.resource,
		)
	}
}

func init() {
	plugins.Index["reservation_state_kpi"] = func() plugins.KPI { return &ReservationStateKPI{} }
}
