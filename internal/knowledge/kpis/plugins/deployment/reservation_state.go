// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package deployment

import (
	"context"
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
type ReservationStateKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[ReservationStateKPIOpts]

	// Prometheus descriptor for the reservation state metric.
	counter *prometheus.Desc
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
	return nil
}

// Conform to the prometheus collector interface by providing the descriptor.
func (k *ReservationStateKPI) Describe(ch chan<- *prometheus.Desc) { ch <- k.counter }

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
	// Aggregate counts by (type, state, reason) so that we emit one time
	// series per bucket rather than one per reservation. This keeps metric
	// cardinality bounded regardless of how many reservations exist.
	type bucket struct {
		reservationType string
		state           string
		reason          string
	}
	counts := map[bucket]float64{}
	for _, r := range reservationList.Items {
		if r.Spec.SchedulingDomain != k.Options.ReservationSchedulingDomain {
			continue
		}
		state := "unknown"
		reason := ""
		cond := meta.FindStatusCondition(r.Status.Conditions, v1alpha1.ReservationConditionReady)
		if cond != nil {
			reason = cond.Reason
			switch cond.Status {
			case metav1.ConditionTrue:
				state = "ready"
			case metav1.ConditionFalse:
				state = "error"
			default:
				state = "unknown"
			}
		}
		counts[bucket{
			reservationType: string(r.Spec.Type),
			state:           state,
			reason:          reason,
		}]++
	}
	for b, v := range counts {
		ch <- prometheus.MustNewConstMetric(
			k.counter, prometheus.GaugeValue, v,
			string(k.Options.ReservationSchedulingDomain),
			b.reservationType, b.state, b.reason,
		)
	}
}
