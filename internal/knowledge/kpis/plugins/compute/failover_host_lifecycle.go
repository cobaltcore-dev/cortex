// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"context"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	vmv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var failoverHostLifecycleKPILogger = ctrl.Log.WithName("failover-host-lifecycle-kpi")

// novaStatusDeleted is the Nova status of a deleted server, which no longer
// occupies its host.
const novaStatusDeleted = "DELETED"

// FailoverHostLifecycleKPI exposes the lifecycle state of failover host
// reservations as three fleet-wide gauges, derived from the reservations and
// virtual machines present in the cluster:
//
//   - cortex_failover_host_reservations_draining — reservations present but not
//     yet FailoverHostActive (their reserved host is still being emptied).
//   - cortex_failover_host_reservations_reoccupied — Active, not-in-use
//     reservations whose host nonetheless has a virtual machine on it. The filter
//     pipeline should keep ordinary workloads off reserved hosts, so this is an
//     invariant violation that the drain controller deliberately does not
//     auto-correct; the gauge is the signal an alert escalates on. In-use hosts
//     are excluded, as a gang evacuation legitimately holds VMs there.
//   - cortex_failover_host_reservations_evacuating — in-use reservations whose
//     source host still has a virtual machine on it. A persistently non-zero
//     value means a gang evacuation is not draining its source host, so the
//     completion controller never gets to release the reservation.
//
// All are pure reads over k8s resources, so they live here rather than in the
// controllers' reconcile loops, where they would add a list per reconcile and
// couple the metric to informer timing.
type FailoverHostLifecycleKPI struct {
	plugins.BaseKPI[struct{}]

	draining   *prometheus.Desc
	reoccupied *prometheus.Desc
	evacuating *prometheus.Desc
}

func (FailoverHostLifecycleKPI) GetName() string { return "failover_host_lifecycle_kpi" }

func (k *FailoverHostLifecycleKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.draining = prometheus.NewDesc(
		"cortex_failover_host_reservations_draining",
		"Number of failover host reservations present but not yet FailoverHostActive (their reserved host is still draining)",
		nil, nil,
	)
	k.reoccupied = prometheus.NewDesc(
		"cortex_failover_host_reservations_reoccupied",
		"Number of active, not-in-use failover host reservations whose host has a virtual machine on it; a filter-pipeline leak the drain controller does not auto-correct",
		nil, nil,
	)
	k.evacuating = prometheus.NewDesc(
		"cortex_failover_host_reservations_evacuating",
		"Number of in-use failover host reservations whose source host still has a virtual machine on it; a persistently non-zero value means a gang evacuation is not draining its source host",
		nil, nil,
	)
	return nil
}

func (k *FailoverHostLifecycleKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.draining
	ch <- k.reoccupied
	ch <- k.evacuating
}

func (k *FailoverHostLifecycleKPI) Collect(ch chan<- prometheus.Metric) {
	// Bound the list calls so a slow API server can't hang the Prometheus scrape
	// indefinitely; if they fail we log so the disappearance of the metrics is
	// not silent.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	reservations := &v1alpha1.ReservationList{}
	if err := k.Client.List(ctx, reservations); err != nil {
		failoverHostLifecycleKPILogger.Error(err, "Failed to list reservations")
		return
	}
	vms := &vmv1alpha1.VirtualMachineList{}
	if err := k.Client.List(ctx, vms); err != nil {
		failoverHostLifecycleKPILogger.Error(err, "Failed to list virtual machines")
		return
	}
	occupied := map[string]bool{}
	for i := range vms.Items {
		if h := occupiedHost(&vms.Items[i]); h != "" {
			occupied[h] = true
		}
	}
	var draining, reoccupied, evacuating float64
	for _, res := range reservations.Items {
		if res.Spec.Type != v1alpha1.ReservationTypeFailoverHost {
			continue
		}
		// In use with a still-occupied source host: the evacuation is not
		// draining its source, so completion cannot release the reservation.
		if meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostInUse) &&
			res.Status.FailoverHostReservation != nil &&
			res.Status.FailoverHostReservation.SourceHost != "" &&
			occupied[res.Status.FailoverHostReservation.SourceHost] {
			evacuating++
		}
		if !meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostActive) {
			draining++
			continue
		}
		// Active: flag it only if a VM occupies the host and no evacuation claims it.
		if occupied[res.Spec.TargetHost] &&
			!meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostInUse) {
			reoccupied++
		}
	}
	ch <- prometheus.MustNewConstMetric(k.draining, prometheus.GaugeValue, draining)
	ch <- prometheus.MustNewConstMetric(k.reoccupied, prometheus.GaugeValue, reoccupied)
	ch <- prometheus.MustNewConstMetric(k.evacuating, prometheus.GaugeValue, evacuating)
}

// occupiedHost returns the host a VM keeps from being empty: the host Nova
// reports it on, or "" if it is not placed yet, being pruned or deleted in Nova.
func occupiedHost(vm *vmv1alpha1.VirtualMachine) string {
	if vm.Status.Nova == nil || vm.Status.DeletionTime != nil || vm.Status.Nova.Status == novaStatusDeleted {
		return ""
	}
	return vm.Status.Nova.Host
}

func init() {
	plugins.Index["failover_host_lifecycle_kpi"] = func() plugins.KPI { return &FailoverHostLifecycleKPI{} }
}
