// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var crControllerMonitorLog = ctrl.Log.WithName("committed-resource-controller-monitor").WithValues("module", "committed-resources")

// CRControllerMonitor reports the number of CommittedResource CRDs that are
// actively awaiting reservation placement (Reason=Reserving, AllowRejection=false).
//
// This includes both CRs on their first placement attempt and those being retried
// after a failure. API-originated dry-run probes (AllowRejection=true) are excluded.
// The metric is absent for a given label set when no CRs match — absence means zero.
type CRControllerMonitor struct {
	client              client.Client
	unfulfilled         *prometheus.Desc
	slotLimitRejections *prometheus.CounterVec
}

func NewCRControllerMonitor(c client.Client) CRControllerMonitor {
	return CRControllerMonitor{
		client: c,
		unfulfilled: prometheus.NewDesc(
			"cortex_committed_resource_unfulfilled",
			"Number of CommittedResource CRDs that the controller cannot fully satisfy with reservation slots.",
			[]string{"flavor_group", "resource_type", "availability_zone"},
			nil,
		),
		slotLimitRejections: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_committed_resource_slot_limit_rejections_total",
			Help: "Number of times a commitment was rejected because the requested slot count exceeded the configured limit.",
		}, []string{"flavor_group", "availability_zone"}),
	}
}

// RecordSlotLimitRejection increments the slot-limit rejection counter for the given flavor group and AZ.
func (m *CRControllerMonitor) RecordSlotLimitRejection(flavorGroup, az string) {
	m.slotLimitRejections.WithLabelValues(flavorGroup, az).Inc()
}

// Describe implements prometheus.Collector.
func (m *CRControllerMonitor) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.unfulfilled
	m.slotLimitRejections.Describe(ch)
}

// Collect implements prometheus.Collector. Lists all CommittedResource CRDs and counts
// those with Ready=False/Reason=Reserving and AllowRejection=false, grouped by flavor group, resource type, and AZ.
func (m *CRControllerMonitor) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var list v1alpha1.CommittedResourceList
	if err := m.client.List(ctx, &list); err != nil {
		crControllerMonitorLog.Error(err, "failed to list CommittedResources")
		return
	}

	type key struct{ flavorGroup, resourceType, az string }
	counts := make(map[key]int)
	for _, cr := range list.Items {
		if cr.Spec.AllowRejection {
			continue
		}
		cond := meta.FindStatusCondition(cr.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
		if cond == nil || cond.Reason != v1alpha1.CommittedResourceReasonReserving {
			continue
		}
		counts[key{
			flavorGroup:  cr.Spec.FlavorGroupName,
			resourceType: string(cr.Spec.ResourceType),
			az:           cr.Spec.AvailabilityZone,
		}]++
	}

	for k, count := range counts {
		ch <- prometheus.MustNewConstMetric(
			m.unfulfilled,
			prometheus.GaugeValue,
			float64(count),
			k.flavorGroup, k.resourceType, k.az,
		)
	}
	m.slotLimitRejections.Collect(ch)
}
