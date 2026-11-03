// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"context"
	"slices"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// matcherReservation builds a failover host reservation targeting targetHost
// and able to rescue the given source hosts.
func matcherReservation(targetHost string, sourceHosts ...string) *v1alpha1.Reservation {
	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "failover-host-" + targetHost},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeFailoverHost,
			TargetHost: targetHost,
			FailoverHostReservation: &v1alpha1.FailoverHostReservationSpec{
				SourceHosts: sourceHosts,
			},
		},
	}
}

// inUse marks a reservation as in use by the given source host.
func inUse(res *v1alpha1.Reservation, sourceHost string) *v1alpha1.Reservation {
	res.Status.FailoverHostReservation = &v1alpha1.FailoverHostReservationStatus{SourceHost: sourceHost}
	meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
		Type:   v1alpha1.ConditionTypeFailoverHostInUse,
		Status: metav1.ConditionTrue,
		Reason: "EvacuationStarted",
	})
	return res
}

func evacuationRequest(sourceHost string) api.ExternalSchedulerRequest {
	hints := map[string]any{"_nova_check_type": "evacuate"}
	if sourceHost != "" {
		hints["source_host"] = sourceHost
	}
	return api.ExternalSchedulerRequest{
		Spec: api.NovaObject[api.NovaSpec]{Data: api.NovaSpec{
			AvailabilityZone: "az1",
			SchedulerHints:   hints,
		}},
	}
}

func getReservation(t *testing.T, c client.Client) v1alpha1.Reservation {
	t.Helper()
	var res v1alpha1.Reservation
	if err := c.Get(context.Background(), types.NamespacedName{Name: "failover-host-target1"}, &res); err != nil {
		t.Fatalf("failed to get reservation: %v", err)
	}
	return res
}

func TestMatcher_Match(t *testing.T) {
	tests := []struct {
		name         string
		reservations []*v1alpha1.Reservation
		request      api.ExternalSchedulerRequest
		orig         []string
		want         []string
		// check runs after Match to assert reservation state in the client.
		check func(t *testing.T, c client.Client)
	}{
		{
			name:         "non-evacuation is a no-op",
			reservations: []*v1alpha1.Reservation{matcherReservation("target1", "source1")},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{Data: api.NovaSpec{
					SchedulerHints: map[string]any{"_nova_check_type": "live_migrate", "source_host": "source1"},
				}},
			},
			orig: []string{"host1", "host2"},
			want: []string{"host1", "host2"},
		},
		{
			name:         "evacuation without source hint is a no-op",
			reservations: []*v1alpha1.Reservation{matcherReservation("target1", "source1")},
			request:      evacuationRequest(""),
			orig:         []string{"host1", "host2"},
			want:         []string{"host1", "host2"},
		},
		{
			name:         "no matching reservation falls back to original candidates",
			reservations: []*v1alpha1.Reservation{matcherReservation("target1", "otherSource")},
			request:      evacuationRequest("source1"),
			orig:         []string{"host1", "host2"},
			want:         []string{"host1", "host2"},
		},
		{
			name:         "pins a fresh reservation and writes InUse",
			reservations: []*v1alpha1.Reservation{matcherReservation("target1", "source1")},
			request:      evacuationRequest("source1"),
			orig:         []string{"host1", "target1", "host2"},
			want:         []string{"target1"},
			check: func(t *testing.T, c client.Client) {
				res := getReservation(t, c)
				if !meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostInUse) {
					t.Error("expected InUse condition to be True")
				}
				if res.Status.FailoverHostReservation == nil || res.Status.FailoverHostReservation.SourceHost != "source1" {
					t.Errorf("expected status SourceHost=source1, got %+v", res.Status.FailoverHostReservation)
				}
			},
		},
		{
			name: "reuses the in-use reservation for the same source",
			// A second, not-in-use reservation (target0 sorts first) must be
			// ignored in favour of the one already in use by this source.
			reservations: []*v1alpha1.Reservation{
				inUse(matcherReservation("target1", "source1"), "source1"),
				matcherReservation("target0", "source1"),
			},
			request: evacuationRequest("source1"),
			orig:    []string{"target0", "target1", "host2"},
			want:    []string{"target1"},
		},
		{
			name: "does not steal a reservation in use by another source",
			// source2 evacuates while source1 already holds target1, so source2
			// must pin the free target2 and leave target1's pin intact.
			reservations: []*v1alpha1.Reservation{
				inUse(matcherReservation("target1", "source1", "source2"), "source1"),
				matcherReservation("target2", "source1", "source2"),
			},
			request: evacuationRequest("source2"),
			orig:    []string{"target1", "target2", "host3"},
			want:    []string{"target2"},
			check: func(t *testing.T, c client.Client) {
				res := getReservation(t, c)
				if res.Status.FailoverHostReservation == nil || res.Status.FailoverHostReservation.SourceHost != "source1" {
					t.Errorf("expected target1 to stay pinned to source1, got %+v", res.Status.FailoverHostReservation)
				}
			},
		},
		{
			name: "all reservations in use by others falls back",
			reservations: []*v1alpha1.Reservation{
				inUse(matcherReservation("target1", "source1", "source2"), "source1"),
			},
			request: evacuationRequest("source2"),
			orig:    []string{"target1", "host3"},
			want:    []string{"target1", "host3"},
			check: func(t *testing.T, c client.Client) {
				res := getReservation(t, c)
				if res.Status.FailoverHostReservation.SourceHost != "source1" {
					t.Errorf("expected target1 to stay pinned to source1, got %q", res.Status.FailoverHostReservation.SourceHost)
				}
			},
		},
		{
			name:         "chosen host not in candidates falls back but still pins",
			reservations: []*v1alpha1.Reservation{matcherReservation("target1", "source1")},
			request:      evacuationRequest("source1"),
			orig:         []string{"host1", "host2"}, // target1 absent: conflicts with other constraints
			want:         []string{"host1", "host2"},
			check: func(t *testing.T, c client.Client) {
				res := getReservation(t, c)
				if !meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostInUse) {
					t.Error("expected InUse condition to be True even on candidate fallback")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := make([]client.Object, len(tt.reservations))
			for i, r := range tt.reservations {
				objs[i] = r
			}
			c := newTestClient(t, objs...)
			m := &Matcher{Client: c, Monitor: NewMatcherMonitor()}
			got, err := m.Match(context.Background(), tt.request, tt.orig)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			if tt.check != nil {
				tt.check(t, c)
			}
		})
	}
}
