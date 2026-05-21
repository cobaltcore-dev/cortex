// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var crControllerMonitorLog = ctrl.Log.WithName("committed-resource-controller-monitor").WithValues("module", "committed-resources")

// CRControllerMonitor reports the number of CommittedResource CRDs that the controller
// is currently unable to fully satisfy with reservation slots.
//
// Only CRs on the AllowRejection=false retry path are counted — those created by the
// syncer for existing Limes commitments. API-originated rejections (AllowRejection=true)
// and dry-run probes never enter this state.
type CRControllerMonitor struct {
	client      client.Client
	unfulfilled *prometheus.Desc
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
	}
}

// Describe implements prometheus.Collector.
func (m *CRControllerMonitor) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.unfulfilled
}

// Collect implements prometheus.Collector. Lists all CommittedResource CRDs and counts
// those with Ready=False/Reason=Reserving, grouped by flavor group, resource type, and AZ.
func (m *CRControllerMonitor) Collect(ch chan<- prometheus.Metric) {
	var list v1alpha1.CommittedResourceList
	if err := m.client.List(context.Background(), &list); err != nil {
		crControllerMonitorLog.Error(err, "failed to list CommittedResources")
		return
	}

	type key struct{ flavorGroup, resourceType, az string }
	counts := make(map[key]int)
	for _, cr := range list.Items {
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
}
