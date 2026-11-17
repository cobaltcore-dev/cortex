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
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// setActive marks a reservation FailoverHostActive=True.
func setActive(r *v1alpha1.Reservation) *v1alpha1.Reservation {
	meta.SetStatusCondition(&r.Status.Conditions, v1.Condition{
		Type: v1alpha1.ConditionTypeFailoverHostActive, Status: v1.ConditionTrue, Reason: v1alpha1.ReasonFailoverHostDrained,
	})
	return r
}

// setInUse marks a reservation FailoverHostInUse=True.
func setInUse(r *v1alpha1.Reservation) *v1alpha1.Reservation {
	meta.SetStatusCondition(&r.Status.Conditions, v1.Condition{
		Type: v1alpha1.ConditionTypeFailoverHostInUse, Status: v1.ConditionTrue, Reason: "Evacuating",
	})
	return r
}

func TestFailoverHostLifecycleKPI_GetName(t *testing.T) {
	kpi := &FailoverHostLifecycleKPI{}
	if name := kpi.GetName(); name != "failover_host_lifecycle_kpi" {
		t.Errorf("expected name failover_host_lifecycle_kpi, got %q", name)
	}
}

func TestFailoverHostLifecycleKPI_Collect(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("build scheme: %v", err)
	}
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add hv scheme: %v", err)
	}
	if err := vmv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add vm scheme: %v", err)
	}

	// draining: not yet Active.
	draining := failoverHostReservation("failover-host-d", "d", "peer")
	// activeEmpty: Active, no VM -> neither draining nor reoccupied.
	activeEmpty := setActive(ptr(failoverHostReservation("failover-host-e", "e", "peer")))
	// reoccupied: Active, not in use, but a VM sits on its host -> reoccupied.
	reoccupied := setActive(ptr(failoverHostReservation("failover-host-r", "r", "peer")))
	// activeInUse: Active AND in use with a VM -> legitimate evacuee, not flagged.
	activeInUse := setInUse(setActive(ptr(failoverHostReservation("failover-host-u", "u", "peer"))))
	// evacuating: in use with a recorded source host whose hypervisor is not yet
	// evicted.
	evacuating := setInUse(setActive(ptr(failoverHostReservation("failover-host-ev", "ev", "src"))))
	evacuating.Status.FailoverHostReservation = &v1alpha1.FailoverHostReservationStatus{SourceHost: "src"}
	// A non-failover reservation must be ignored entirely.
	other := v1alpha1.Reservation{
		ObjectMeta: v1.ObjectMeta{Name: "r-other"},
		Spec:       v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeInFlight, TargetHost: "r"},
	}

	vmOnR := virtualMachine("vm-r", "r")
	vmOnU := virtualMachine("vm-u", "u")
	// The source hypervisor exists but is not evicted yet, so evacuating counts.
	srcHV := hv1.Hypervisor{ObjectMeta: v1.ObjectMeta{Name: "src"}, Status: hv1.HypervisorStatus{Evicted: false}}

	client := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(&draining, activeEmpty, reoccupied, activeInUse, evacuating, &other, &vmOnR, &vmOnU, &srcHV).Build()

	kpi := &FailoverHostLifecycleKPI{}
	if err := kpi.Init(nil, client, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("init: %v", err)
	}
	ch := make(chan prometheus.Metric, 8)
	kpi.Collect(ch)
	close(ch)

	got := map[string]float64{}
	for m := range ch {
		var metric dto.Metric
		if err := m.Write(&metric); err != nil {
			t.Fatalf("write metric: %v", err)
		}
		switch m.Desc().String() {
		case kpi.draining.String():
			got["draining"] = metric.GetGauge().GetValue()
		case kpi.reoccupied.String():
			got["reoccupied"] = metric.GetGauge().GetValue()
		case kpi.evacuating.String():
			got["evacuating"] = metric.GetGauge().GetValue()
		default:
			t.Errorf("unexpected metric %s", m.Desc().String())
		}
	}
	if got["draining"] != 1 {
		t.Errorf("draining = %v, want 1", got["draining"])
	}
	if got["reoccupied"] != 1 {
		t.Errorf("reoccupied = %v, want 1", got["reoccupied"])
	}
	if got["evacuating"] != 1 {
		t.Errorf("evacuating = %v, want 1", got["evacuating"])
	}
}

func ptr(r v1alpha1.Reservation) *v1alpha1.Reservation { return &r }
