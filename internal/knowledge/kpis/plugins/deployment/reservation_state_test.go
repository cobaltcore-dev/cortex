// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package deployment

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
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
	ch := make(chan *prometheus.Desc, 1)
	kpi.Describe(ch)
	close(ch)
	descCount := 0
	for range ch {
		descCount++
	}
	if descCount != 1 {
		t.Errorf("expected 1 descriptor, got %d", descCount)
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

			metricsCount := 0
			for range ch {
				metricsCount++
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
