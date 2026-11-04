// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"context"
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

func getReservation(t *testing.T, c client.Client, name string) v1alpha1.Reservation {
	t.Helper()
	var res v1alpha1.Reservation
	if err := c.Get(context.Background(), types.NamespacedName{Name: name}, &res); err != nil {
		t.Fatalf("failed to get reservation: %v", err)
	}
	return res
}

func isInUseBy(res v1alpha1.Reservation, sourceHost string) bool {
	if !meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostInUse) {
		return false
	}
	return res.Status.FailoverHostReservation != nil &&
		res.Status.FailoverHostReservation.SourceHost == sourceHost
}

func TestMatcher_ConfirmAndMarkInUse(t *testing.T) {
	tests := []struct {
		name         string
		reservations []*v1alpha1.Reservation
		request      api.ExternalSchedulerRequest
		orderedHosts []string
		// check runs after the call to assert reservation state in the client.
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
			orderedHosts: []string{"target1", "host2"},
			check: func(t *testing.T, c client.Client) {
				res := getReservation(t, c, "failover-host-target1")
				if meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostInUse) {
					t.Error("expected target1 to not be in use for a non-evacuation request")
				}
			},
		},
		{
			name:         "evacuation without source hint is a no-op",
			reservations: []*v1alpha1.Reservation{matcherReservation("target1", "source1")},
			request:      evacuationRequest(""),
			orderedHosts: []string{"target1", "host2"},
			check: func(t *testing.T, c client.Client) {
				res := getReservation(t, c, "failover-host-target1")
				if meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostInUse) {
					t.Error("expected target1 to not be in use without a source hint")
				}
			},
		},
		{
			name:         "pins the selected reservation when it is the top candidate",
			reservations: []*v1alpha1.Reservation{matcherReservation("target1", "source1")},
			request:      evacuationRequest("source1"),
			orderedHosts: []string{"target1", "host1", "host2"},
			check: func(t *testing.T, c client.Client) {
				res := getReservation(t, c, "failover-host-target1")
				if !isInUseBy(res, "source1") {
					t.Errorf("expected target1 to be in use by source1, got %+v", res.Status.FailoverHostReservation)
				}
			},
		},
		{
			name:         "does not pin when the selected target is not the top candidate",
			reservations: []*v1alpha1.Reservation{matcherReservation("target1", "source1")},
			request:      evacuationRequest("source1"),
			// target1 lost to an ordinary host (e.g. filtered out by other
			// constraints), so the evacuation falls back and must not pin.
			orderedHosts: []string{"host1", "host2"},
			check: func(t *testing.T, c client.Client) {
				res := getReservation(t, c, "failover-host-target1")
				if meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostInUse) {
					t.Error("expected target1 to NOT be pinned when it is not the top candidate")
				}
			},
		},
		{
			name: "reuses the in-use reservation for the same source, no new write",
			reservations: []*v1alpha1.Reservation{
				inUse(matcherReservation("target1", "source1"), "source1"),
				matcherReservation("target0", "source1"),
			},
			request:      evacuationRequest("source1"),
			orderedHosts: []string{"target1", "host2"},
			check: func(t *testing.T, c client.Client) {
				res := getReservation(t, c, "failover-host-target1")
				if !isInUseBy(res, "source1") {
					t.Errorf("expected target1 to stay in use by source1, got %+v", res.Status.FailoverHostReservation)
				}
				// target0 must remain untouched.
				res0 := getReservation(t, c, "failover-host-target0")
				if meta.IsStatusConditionTrue(res0.Status.Conditions, v1alpha1.ConditionTypeFailoverHostInUse) {
					t.Error("expected target0 to remain free")
				}
			},
		},
		{
			name: "does not steal a reservation in use by another source",
			reservations: []*v1alpha1.Reservation{
				inUse(matcherReservation("target1", "source1", "source2"), "source1"),
				matcherReservation("target2", "source1", "source2"),
			},
			request:      evacuationRequest("source2"),
			orderedHosts: []string{"target2", "host3"},
			check: func(t *testing.T, c client.Client) {
				res1 := getReservation(t, c, "failover-host-target1")
				if !isInUseBy(res1, "source1") {
					t.Errorf("expected target1 to stay pinned to source1, got %+v", res1.Status.FailoverHostReservation)
				}
				res2 := getReservation(t, c, "failover-host-target2")
				if !isInUseBy(res2, "source2") {
					t.Errorf("expected target2 to be pinned to source2, got %+v", res2.Status.FailoverHostReservation)
				}
			},
		},
		{
			name: "all reservations in use by others is a no-op",
			reservations: []*v1alpha1.Reservation{
				inUse(matcherReservation("target1", "source1", "source2"), "source1"),
			},
			request:      evacuationRequest("source2"),
			orderedHosts: []string{"host3"},
			check: func(t *testing.T, c client.Client) {
				res := getReservation(t, c, "failover-host-target1")
				if res.Status.FailoverHostReservation.SourceHost != "source1" {
					t.Errorf("expected target1 to stay pinned to source1, got %q", res.Status.FailoverHostReservation.SourceHost)
				}
			},
		},
		{
			name:         "no matching reservation is a no-op",
			reservations: []*v1alpha1.Reservation{matcherReservation("target1", "otherSource")},
			request:      evacuationRequest("source1"),
			orderedHosts: []string{"host1", "host2"},
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
			if err := m.ConfirmAndMarkInUse(context.Background(), tt.request, tt.orderedHosts); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, c)
			}
		})
	}
}
