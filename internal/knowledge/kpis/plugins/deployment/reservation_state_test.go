// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package deployment

import (
	"strings"
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

func TestReservationStateKPI_Init(t *testing.T) {
	kpi := &ReservationStateKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts(`{"reservationSchedulingDomain": "nova"}`)); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestReservationStateKPI_GetName(t *testing.T) {
	kpi := &ReservationStateKPI{}
	expectedName := "reservation_state_kpi"
	if name := kpi.GetName(); name != expectedName {
		t.Errorf("expected name %q, got %q", expectedName, name)
	}
}

func TestReservationStateKPI_Describe(t *testing.T) {
	kpi := &ReservationStateKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts(`{"reservationSchedulingDomain": "nova"}`)); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	ch := make(chan *prometheus.Desc, 3)
	kpi.Describe(ch)
	close(ch)
	descCount := 0
	for range ch {
		descCount++
	}
	if descCount != 3 {
		t.Errorf("expected 3 descriptors, got %d", descCount)
	}
}

func TestReservationStateKPI_Collect(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name          string
		reservations  []v1alpha1.Reservation
		operator      v1alpha1.SchedulingDomain
		expectedCount int
		description   string
	}{
		{
			name:          "no reservations",
			reservations:  []v1alpha1.Reservation{},
			operator:      "nova",
			expectedCount: 0,
			description:   "should not collect metrics when no reservations exist",
		},
		{
			name: "single ready in-flight reservation",
			reservations: []v1alpha1.Reservation{
				{
					ObjectMeta: v1.ObjectMeta{Name: "r1"},
					Spec: v1alpha1.ReservationSpec{
						Type:             v1alpha1.ReservationTypeInFlight,
						SchedulingDomain: "nova",
					},
					Status: v1alpha1.ReservationStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.ReservationConditionReady,
								Status: v1.ConditionTrue,
								Reason: "ReservationReady",
							},
						},
					},
				},
			},
			operator:      "nova",
			expectedCount: 1,
			description:   "should collect a single ready metric",
		},
		{
			name: "unknown in-flight reservation with InstanceNotFound reason",
			reservations: []v1alpha1.Reservation{
				{
					ObjectMeta: v1.ObjectMeta{Name: "r2"},
					Spec: v1alpha1.ReservationSpec{
						Type:             v1alpha1.ReservationTypeInFlight,
						SchedulingDomain: "nova",
					},
					Status: v1alpha1.ReservationStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.ReservationConditionReady,
								Status: v1.ConditionUnknown,
								Reason: "InstanceNotFound",
							},
						},
					},
				},
			},
			operator:      "nova",
			expectedCount: 1,
			description:   "should collect a metric labelled with reason=InstanceNotFound",
		},
		{
			name: "reservation without any conditions falls back to unknown",
			reservations: []v1alpha1.Reservation{
				{
					ObjectMeta: v1.ObjectMeta{Name: "r3"},
					Spec: v1alpha1.ReservationSpec{
						Type:             v1alpha1.ReservationTypeInFlight,
						SchedulingDomain: "nova",
					},
				},
			},
			operator:      "nova",
			expectedCount: 1,
			description:   "reservations without conditions should still emit a metric",
		},
		{
			name: "multiple in-flight reservations with the same reason are aggregated",
			reservations: []v1alpha1.Reservation{
				{
					ObjectMeta: v1.ObjectMeta{Name: "r-a"},
					Spec: v1alpha1.ReservationSpec{
						Type:             v1alpha1.ReservationTypeInFlight,
						SchedulingDomain: "nova",
					},
					Status: v1alpha1.ReservationStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.ReservationConditionReady,
								Status: v1.ConditionUnknown,
								Reason: "InstanceNotFound",
							},
						},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "r-b"},
					Spec: v1alpha1.ReservationSpec{
						Type:             v1alpha1.ReservationTypeInFlight,
						SchedulingDomain: "nova",
					},
					Status: v1alpha1.ReservationStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.ReservationConditionReady,
								Status: v1.ConditionUnknown,
								Reason: "InstanceNotFound",
							},
						},
					},
				},
			},
			operator:      "nova",
			expectedCount: 1,
			description:   "two reservations with the same (type,state,reason) should share one time series",
		},
		{
			name: "different reservation types emit separate metrics",
			reservations: []v1alpha1.Reservation{
				{
					ObjectMeta: v1.ObjectMeta{Name: "r-if"},
					Spec: v1alpha1.ReservationSpec{
						Type:             v1alpha1.ReservationTypeInFlight,
						SchedulingDomain: "nova",
					},
					Status: v1alpha1.ReservationStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.ReservationConditionReady,
								Status: v1.ConditionTrue,
								Reason: "ReservationReady",
							},
						},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "r-cr"},
					Spec: v1alpha1.ReservationSpec{
						Type:             v1alpha1.ReservationTypeCommittedResource,
						SchedulingDomain: "nova",
					},
					Status: v1alpha1.ReservationStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.ReservationConditionReady,
								Status: v1.ConditionTrue,
								Reason: "ReservationReady",
							},
						},
					},
				},
			},
			operator:      "nova",
			expectedCount: 2,
			description:   "in-flight and committed-resource reservations should be labelled separately",
		},
		{
			name: "filter by scheduling domain",
			reservations: []v1alpha1.Reservation{
				{
					ObjectMeta: v1.ObjectMeta{Name: "r-nova"},
					Spec: v1alpha1.ReservationSpec{
						Type:             v1alpha1.ReservationTypeInFlight,
						SchedulingDomain: "nova",
					},
					Status: v1alpha1.ReservationStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.ReservationConditionReady,
								Status: v1.ConditionTrue,
							},
						},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "r-other"},
					Spec: v1alpha1.ReservationSpec{
						Type:             v1alpha1.ReservationTypeInFlight,
						SchedulingDomain: "ironcore",
					},
					Status: v1alpha1.ReservationStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.ReservationConditionReady,
								Status: v1.ConditionTrue,
							},
						},
					},
				},
			},
			operator:      "nova",
			expectedCount: 1,
			description:   "only reservations matching the configured domain should be counted",
		},
		{
			name: "error reservation is labelled state=error",
			reservations: []v1alpha1.Reservation{
				{
					ObjectMeta: v1.ObjectMeta{Name: "r-err"},
					Spec: v1alpha1.ReservationSpec{
						Type:             v1alpha1.ReservationTypeInFlight,
						SchedulingDomain: "nova",
					},
					Status: v1alpha1.ReservationStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.ReservationConditionReady,
								Status: v1.ConditionFalse,
								Reason: "UnexpectedType",
							},
						},
					},
				},
			},
			operator:      "nova",
			expectedCount: 1,
			description:   "false Ready condition should surface as state=error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]v1alpha1.Reservation, len(tt.reservations))
			copy(objects, tt.reservations)

			clientBuilder := fake.NewClientBuilder().WithScheme(scheme)
			for i := range objects {
				clientBuilder = clientBuilder.WithObjects(&objects[i])
			}
			client := clientBuilder.Build()

			kpi := &ReservationStateKPI{}
			if err := kpi.Init(nil, client, conf.NewRawOpts(`{"reservationSchedulingDomain": "`+string(tt.operator)+`"}`)); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			ch := make(chan prometheus.Metric, 16)
			kpi.Collect(ch)
			close(ch)

			// Count only cortex_reservation_state series; the expectedCount values
			// describe that metric's (type,state,reason) buckets. The ported
			// cortex_reservation_count / _allocated_resources series are asserted
			// separately in TestReservationStateKPI_CollectPortedMetrics.
			metricsCount := 0
			for m := range ch {
				if strings.Contains(m.Desc().String(), "cortex_reservation_state") {
					metricsCount++
				}
			}
			if metricsCount != tt.expectedCount {
				t.Errorf("%s: expected %d metrics, got %d", tt.description, tt.expectedCount, metricsCount)
			}
		})
	}
}

// TestReservationStateKPI_CollectLabels verifies that the metric emitted for
// an in-flight reservation with reason InstanceNotFound carries the labels
// the InFlightReservationUnready alert relies on.
func TestReservationStateKPI_CollectLabels(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	res := &v1alpha1.Reservation{
		ObjectMeta: v1.ObjectMeta{Name: "r-nf"},
		Spec: v1alpha1.ReservationSpec{
			Type:             v1alpha1.ReservationTypeInFlight,
			SchedulingDomain: "nova",
		},
		Status: v1alpha1.ReservationStatus{
			Conditions: []v1.Condition{
				{
					Type:   v1alpha1.ReservationConditionReady,
					Status: v1.ConditionUnknown,
					Reason: "InstanceNotFound",
				},
			},
		},
	}
	// Also register a second identical reservation so that we can assert the
	// counter carries the aggregate count (2).
	res2 := res.DeepCopy()
	res2.Name = "r-nf-2"

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(res, res2).Build()

	kpi := &ReservationStateKPI{}
	if err := kpi.Init(nil, client, conf.NewRawOpts(`{"reservationSchedulingDomain": "nova"}`)); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	ch := make(chan prometheus.Metric, 4)
	kpi.Collect(ch)
	close(ch)

	found := false
	for m := range ch {
		// Scope to cortex_reservation_state; cortex_reservation_count carries a
		// type label too but no state/reason.
		if !strings.Contains(m.Desc().String(), "cortex_reservation_state") {
			continue
		}
		var metric dto.Metric
		if err := m.Write(&metric); err != nil {
			t.Fatalf("failed to write metric: %v", err)
		}
		labels := map[string]string{}
		for _, l := range metric.Label {
			labels[l.GetName()] = l.GetValue()
		}
		if labels["type"] != string(v1alpha1.ReservationTypeInFlight) {
			continue
		}
		found = true
		if labels["domain"] != "nova" {
			t.Errorf("expected domain=nova, got %q", labels["domain"])
		}
		if labels["state"] != "unknown" {
			t.Errorf("expected state=unknown, got %q", labels["state"])
		}
		if labels["reason"] != "InstanceNotFound" {
			t.Errorf("expected reason=InstanceNotFound, got %q", labels["reason"])
		}
		if got := metric.Gauge.GetValue(); got != 2 {
			t.Errorf("expected aggregate count 2, got %f", got)
		}
	}
	if !found {
		t.Fatal("expected an in-flight reservation metric to be collected")
	}
}

// TestReservationStateKPI_CollectPortedMetrics verifies the two metrics ported
// from the former reservations monitor: cortex_reservation_count (by type) and
// cortex_reservation_allocated_resources (by type, host, resource). Both are
// domain-filtered like the rest of the KPI.
func TestReservationStateKPI_CollectPortedMetrics(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// One ready committed-resource reservation on host-1 with resources, plus a
	// reservation in a different domain that must be excluded from every metric.
	res := &v1alpha1.Reservation{
		ObjectMeta: v1.ObjectMeta{Name: "r-cr"},
		Spec: v1alpha1.ReservationSpec{
			Type:             v1alpha1.ReservationTypeCommittedResource,
			SchedulingDomain: "nova",
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: *resource.NewQuantity(16, resource.DecimalSI),
				hv1.ResourceCPU:    *resource.NewQuantity(4, resource.DecimalSI),
			},
		},
		Status: v1alpha1.ReservationStatus{
			Host: "host-1",
			Conditions: []v1.Condition{
				{Type: v1alpha1.ReservationConditionReady, Status: v1.ConditionTrue, Reason: "ReservationReady"},
			},
		},
	}
	resOtherDomain := &v1alpha1.Reservation{
		ObjectMeta: v1.ObjectMeta{Name: "r-other"},
		Spec: v1alpha1.ReservationSpec{
			Type:             v1alpha1.ReservationTypeCommittedResource,
			SchedulingDomain: "ironcore",
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: *resource.NewQuantity(99, resource.DecimalSI),
			},
		},
		Status: v1alpha1.ReservationStatus{Host: "host-9"},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(res, resOtherDomain).Build()

	kpi := &ReservationStateKPI{}
	if err := kpi.Init(nil, client, conf.NewRawOpts(`{"reservationSchedulingDomain": "nova"}`)); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	ch := make(chan prometheus.Metric, 16)
	kpi.Collect(ch)
	close(ch)

	var countSeries, resourceSeries int
	resourceByName := map[string]float64{}
	for m := range ch {
		desc := m.Desc().String()
		var metric dto.Metric
		if err := m.Write(&metric); err != nil {
			t.Fatalf("failed to write metric: %v", err)
		}
		labels := map[string]string{}
		for _, l := range metric.Label {
			labels[l.GetName()] = l.GetValue()
		}
		// No series from the excluded ironcore reservation should appear.
		if labels["host"] == "host-9" || labels["domain"] == "ironcore" {
			t.Errorf("cross-domain reservation leaked into metric: %v", labels)
		}
		switch {
		case strings.Contains(desc, "cortex_reservation_count"):
			countSeries++
			if labels["type"] != string(v1alpha1.ReservationTypeCommittedResource) {
				t.Errorf("expected type label on count metric, got %q", labels["type"])
			}
		case strings.Contains(desc, "cortex_reservation_allocated_resources"):
			resourceSeries++
			if labels["host"] != "host-1" {
				t.Errorf("expected host=host-1, got %q", labels["host"])
			}
			resourceByName[labels["resource"]] = metric.Gauge.GetValue()
		}
	}

	if countSeries != 1 {
		t.Errorf("expected 1 count series, got %d", countSeries)
	}
	if resourceSeries != 2 {
		t.Errorf("expected 2 resource series (memory, cpu), got %d", resourceSeries)
	}
	if got := resourceByName[string(hv1.ResourceMemory)]; got != 16 {
		t.Errorf("expected memory=16, got %v", got)
	}
	if got := resourceByName[string(hv1.ResourceCPU)]; got != 4 {
		t.Errorf("expected cpu=4, got %v", got)
	}
}
