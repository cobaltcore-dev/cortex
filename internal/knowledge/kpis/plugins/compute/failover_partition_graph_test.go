// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func failoverHostReservation(name, target, az string, sources ...string) v1alpha1.Reservation {
	return v1alpha1.Reservation{
		ObjectMeta: v1.ObjectMeta{Name: name},
		Spec: v1alpha1.ReservationSpec{
			Type:             v1alpha1.ReservationTypeFailoverHost,
			SchedulingDomain: "nova",
			AvailabilityZone: az,
			TargetHost:       target,
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: resource.MustParse("256Gi"),
				hv1.ResourceCPU:    resource.MustParse("64"),
			},
			FailoverHostReservation: &v1alpha1.FailoverHostReservationSpec{
				SourceHosts: sources,
			},
		},
	}
}

func TestFailoverPartitionGraphKPI_GetName(t *testing.T) {
	kpi := &FailoverPartitionGraphKPI{}
	if name := kpi.GetName(); name != "failover_partition_graph_kpi" {
		t.Errorf("expected name failover_partition_graph_kpi, got %q", name)
	}
}

func TestFailoverPartitionGraphKPI_Describe(t *testing.T) {
	kpi := &FailoverPartitionGraphKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	ch := make(chan *prometheus.Desc, 2)
	kpi.Describe(ch)
	close(ch)
	count := 0
	for range ch {
		count++
	}
	if count != 2 {
		t.Errorf("expected 2 descriptors, got %d", count)
	}
}

func TestFailoverPartitionGraphKPI_Collect(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Two failover-host reservations sharing source host h1 (so the node for h1
	// is never emitted, but its edges to both targets are), plus a non-failover
	// reservation that must be ignored.
	resA := failoverHostReservation("failover-host-t1", "t1", "az-a", "h1", "h2")
	resB := failoverHostReservation("failover-host-t2", "t2", "az-b", "h1", "h3")
	other := v1alpha1.Reservation{
		ObjectMeta: v1.ObjectMeta{Name: "r-other"},
		Spec: v1alpha1.ReservationSpec{
			Type:             v1alpha1.ReservationTypeInFlight,
			SchedulingDomain: "nova",
			TargetHost:       "ignored",
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(&resA, &resB, &other).Build()

	kpi := &FailoverPartitionGraphKPI{}
	if err := kpi.Init(nil, client, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan prometheus.Metric, 16)
	kpi.Collect(ch)
	close(ch)

	var nodes, edges int
	nodeAZ := map[string]string{}
	edgeSet := map[string]bool{}
	for m := range ch {
		var metric dto.Metric
		if err := m.Write(&metric); err != nil {
			t.Fatalf("failed to write metric: %v", err)
		}
		labels := map[string]string{}
		for _, l := range metric.Label {
			labels[l.GetName()] = l.GetValue()
		}
		switch m.Desc().String() {
		case kpi.node.String():
			nodes++
			nodeAZ[labels["host"]] = labels["az"]
			if labels["memory"] != "256Gi" || labels["cpu"] != "64" {
				t.Errorf("node %s: unexpected resources memory=%q cpu=%q", labels["host"], labels["memory"], labels["cpu"])
			}
		case kpi.edge.String():
			edges++
			edgeSet[labels["source"]+"->"+labels["target"]] = true
		default:
			t.Errorf("unexpected metric descriptor %s", m.Desc().String())
		}
	}

	// Nodes: only the two target hosts (t1, t2). Source-only hosts are not nodes.
	if nodes != 2 {
		t.Errorf("expected 2 node metrics, got %d", nodes)
	}
	if nodeAZ["t1"] != "az-a" || nodeAZ["t2"] != "az-b" {
		t.Errorf("unexpected node AZs: %v", nodeAZ)
	}
	// Edges: h1->t1, h2->t1, h1->t2, h3->t2.
	if edges != 4 {
		t.Errorf("expected 4 edge metrics, got %d", edges)
	}
	for _, want := range []string{"h1->t1", "h2->t1", "h1->t2", "h3->t2"} {
		if !edgeSet[want] {
			t.Errorf("missing edge %s, got %v", want, edgeSet)
		}
	}
}
