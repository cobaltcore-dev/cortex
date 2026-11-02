// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	vmv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func failoverHostReservation(name, target string, sources ...string) v1alpha1.Reservation {
	return v1alpha1.Reservation{
		ObjectMeta: v1.ObjectMeta{Name: name},
		Spec: v1alpha1.ReservationSpec{
			Type:             v1alpha1.ReservationTypeFailoverHost,
			SchedulingDomain: "nova",
			TargetHost:       target,
			FailoverHostReservation: &v1alpha1.FailoverHostReservationSpec{
				SourceHosts: sources,
			},
		},
	}
}

func hypervisor(name, az string) hv1.Hypervisor {
	return hv1.Hypervisor{
		ObjectMeta: v1.ObjectMeta{Name: name, Labels: map[string]string{corev1.LabelTopologyZone: az}},
		Status: hv1.HypervisorStatus{
			Capacity: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: resource.MustParse("256Gi"),
				hv1.ResourceCPU:    resource.MustParse("64"),
			},
		},
	}
}

// virtualMachine builds a vm Nova reports as placed on the given host.
func virtualMachine(name, host string) vmv1alpha1.VirtualMachine {
	return vmv1alpha1.VirtualMachine{
		ObjectMeta: v1.ObjectMeta{Name: name},
		Status:     vmv1alpha1.VMStatus{Nova: &vmv1alpha1.NovaStatus{Host: host}},
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
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if err := vmv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Two failover-host reservations sharing source host h1 (so the edge for h1
	// is emitted to both targets), plus a non-failover reservation that must be
	// ignored.
	resA := failoverHostReservation("failover-host-t1", "t1", "h1", "h2")
	resB := failoverHostReservation("failover-host-t2", "t2", "h1", "h3")
	other := v1alpha1.Reservation{
		ObjectMeta: v1.ObjectMeta{Name: "r-other"},
		Spec: v1alpha1.ReservationSpec{
			Type:             v1alpha1.ReservationTypeInFlight,
			SchedulingDomain: "nova",
			TargetHost:       "ignored",
		},
	}
	// The hypervisors backing the graph. h-idle takes part in no reservation, so
	// its node is reported with value 0.
	t1 := hypervisor("t1", "az-a")
	t2 := hypervisor("t2", "az-b")
	h1 := hypervisor("h1", "az-a")
	h2 := hypervisor("h2", "az-a")
	h3 := hypervisor("h3", "az-b")
	idle := hypervisor("h-idle", "az-a")

	// Two vms on h1 and one on t2; every other host (incl. idle) has none, and an
	// unplaced vm must not be counted.
	vm1 := virtualMachine("vm1", "h1")
	vm2 := virtualMachine("vm2", "h1")
	vm3 := virtualMachine("vm3", "t2")
	vmUnplaced := virtualMachine("vm-unplaced", "")

	client := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(&resA, &resB, &other, &t1, &t2, &h1, &h2, &h3, &idle,
			&vm1, &vm2, &vm3, &vmUnplaced).Build()

	kpi := &FailoverPartitionGraphKPI{}
	if err := kpi.Init(nil, client, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan prometheus.Metric, 32)
	kpi.Collect(ch)
	close(ch)

	var nodes, edges int
	nodeAZ := map[string]string{}
	nodeValue := map[string]float64{}
	nodeVMs := map[string]string{}
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
			nodeValue[labels["host"]] = metric.GetGauge().GetValue()
			nodeVMs[labels["host"]] = labels["vms"]
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

	// Nodes: one per hypervisor, including the idle one.
	if nodes != 6 {
		t.Errorf("expected 6 node metrics, got %d", nodes)
	}
	wantAZ := map[string]string{"t1": "az-a", "t2": "az-b", "h1": "az-a", "h2": "az-a", "h3": "az-b", "h-idle": "az-a"}
	for host, az := range wantAZ {
		if nodeAZ[host] != az {
			t.Errorf("node %s: expected az %q, got %q", host, az, nodeAZ[host])
		}
	}
	// Every hypervisor with a failover path reports 1; the idle one reports 0.
	for _, host := range []string{"t1", "t2", "h1", "h2", "h3"} {
		if nodeValue[host] != 1 {
			t.Errorf("node %s: expected value 1, got %v", host, nodeValue[host])
		}
	}
	if nodeValue["h-idle"] != 0 {
		t.Errorf("node h-idle: expected value 0, got %v", nodeValue["h-idle"])
	}
	// The vms label counts placed vms per host: h1 has two, t2 has one, the rest
	// zero. The unplaced vm is not counted anywhere.
	wantVMs := map[string]string{"h1": "2", "t2": "1", "t1": "0", "h2": "0", "h3": "0", "h-idle": "0"}
	for host, want := range wantVMs {
		if nodeVMs[host] != want {
			t.Errorf("node %s: expected vms %q, got %q", host, want, nodeVMs[host])
		}
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
