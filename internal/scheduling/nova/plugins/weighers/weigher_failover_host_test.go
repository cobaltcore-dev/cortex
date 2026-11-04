// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"testing"

	"github.com/go-logr/logr"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// failoverHostReservation builds an Active failover host reservation (the state
// in which it can be selected as an evacuation target).
func failoverHostReservation(targetHost string, sourceHosts ...string) *v1alpha1.Reservation {
	res := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "failover-host-" + targetHost},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeFailoverHost,
			TargetHost: targetHost,
			FailoverHostReservation: &v1alpha1.FailoverHostReservationSpec{
				SourceHosts: sourceHosts,
			},
		},
	}
	meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
		Type:   v1alpha1.ConditionTypeFailoverHostActive,
		Status: metav1.ConditionTrue,
		Reason: "HostDrained",
	})
	return res
}

// notActive drops a reservation out of the Active state (the same as a freshly
// created, still-draining reservation).
func notActive(res *v1alpha1.Reservation) *v1alpha1.Reservation {
	meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
		Type:   v1alpha1.ConditionTypeFailoverHostActive,
		Status: metav1.ConditionFalse,
		Reason: "Draining",
	})
	return res
}

func failoverInUseBy(res *v1alpha1.Reservation, sourceHost string) *v1alpha1.Reservation {
	res.Status.FailoverHostReservation = &v1alpha1.FailoverHostReservationStatus{SourceHost: sourceHost}
	meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
		Type:   v1alpha1.ConditionTypeFailoverHostInUse,
		Status: metav1.ConditionTrue,
		Reason: "EvacuationStarted",
	})
	return res
}

func weigherRequest(hints map[string]any, hosts ...string) api.ExternalSchedulerRequest {
	hvHosts := make([]api.ExternalSchedulerHost, len(hosts))
	for i, h := range hosts {
		hvHosts[i] = api.ExternalSchedulerHost{ComputeHost: h}
	}
	return api.ExternalSchedulerRequest{
		Hosts: hvHosts,
		Spec:  api.NovaObject[api.NovaSpec]{Data: api.NovaSpec{SchedulerHints: hints}},
	}
}

func evacHints(sourceHost string) map[string]any {
	hints := map[string]any{"_nova_check_type": "evacuate"}
	if sourceHost != "" {
		hints["source_host"] = sourceHost
	}
	return hints
}

func TestWeigherFailoverHost_Run(t *testing.T) {
	scheme := buildTestScheme(t)

	tests := []struct {
		name         string
		opts         WeigherFailoverHostOpts
		reservations []client.Object
		request      api.ExternalSchedulerRequest
		// wantTarget is the host expected to receive the dominating weight, or ""
		// when no host should be lifted.
		wantTarget string
	}{
		{
			name:         "disabled - no effect",
			opts:         WeigherFailoverHostOpts{Enabled: false},
			reservations: []client.Object{failoverHostReservation("h1", "source1")},
			request:      weigherRequest(evacHints("source1"), "h1", "h2"),
			wantTarget:   "",
		},
		{
			name:         "non-evacuation - no effect",
			opts:         WeigherFailoverHostOpts{Enabled: true},
			reservations: []client.Object{failoverHostReservation("h1", "source1")},
			request:      weigherRequest(map[string]any{"_nova_check_type": "live_migrate", "source_host": "source1"}, "h1", "h2"),
			wantTarget:   "",
		},
		{
			name:         "evacuation without source hint - no effect",
			opts:         WeigherFailoverHostOpts{Enabled: true},
			reservations: []client.Object{failoverHostReservation("h1", "source1")},
			request:      weigherRequest(evacHints(""), "h1", "h2"),
			wantTarget:   "",
		},
		{
			name:         "evacuation - lifts the name-first matching target",
			opts:         WeigherFailoverHostOpts{Enabled: true},
			reservations: []client.Object{failoverHostReservation("h1", "source1"), failoverHostReservation("h2", "source1")},
			request:      weigherRequest(evacHints("source1"), "h1", "h2", "h3"),
			wantTarget:   "h1",
		},
		{
			name: "evacuation - lifts the in-use target for the same source",
			opts: WeigherFailoverHostOpts{Enabled: true},
			reservations: []client.Object{
				failoverHostReservation("h1", "source1"),
				failoverInUseBy(failoverHostReservation("h2", "source1"), "source1"),
			},
			request:    weigherRequest(evacHints("source1"), "h1", "h2", "h3"),
			wantTarget: "h2",
		},
		{
			name:         "evacuation - no matching reservation - no effect",
			opts:         WeigherFailoverHostOpts{Enabled: true},
			reservations: []client.Object{failoverHostReservation("h1", "otherSource")},
			request:      weigherRequest(evacHints("source1"), "h1", "h2"),
			wantTarget:   "",
		},
		{
			name:         "evacuation - not-yet-active target is not lifted",
			opts:         WeigherFailoverHostOpts{Enabled: true},
			reservations: []client.Object{notActive(failoverHostReservation("h1", "source1"))},
			request:      weigherRequest(evacHints("source1"), "h1", "h2"),
			wantTarget:   "",
		},
		{
			name: "evacuation - lifts the active target over a not-yet-active one",
			opts: WeigherFailoverHostOpts{Enabled: true},
			reservations: []client.Object{
				notActive(failoverHostReservation("h1", "source1")),
				failoverHostReservation("h2", "source1"),
			},
			request:    weigherRequest(evacHints("source1"), "h1", "h2", "h3"),
			wantTarget: "h2",
		},
		{
			name:         "evacuation - selected target not among candidates - no effect",
			opts:         WeigherFailoverHostOpts{Enabled: true},
			reservations: []client.Object{failoverHostReservation("h1", "source1")},
			request:      weigherRequest(evacHints("source1"), "h2", "h3"),
			wantTarget:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &WeigherFailoverHost{}
			step.Options = tt.opts
			step.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.reservations...).Build()

			result, err := step.Run(logr.Discard(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			for host, weight := range result.Activations {
				if host == tt.wantTarget {
					if weight != failoverTargetWeight {
						t.Errorf("expected target %s to get weight %v, got %v", host, failoverTargetWeight, weight)
					}
				} else if weight != 0 {
					t.Errorf("expected host %s to have no effect (0), got %v", host, weight)
				}
			}
			if tt.wantTarget != "" {
				if _, ok := result.Activations[tt.wantTarget]; !ok {
					t.Errorf("expected target %s to be present in activations", tt.wantTarget)
				}
			}
		})
	}
}

func TestWeigherFailoverHost_Index(t *testing.T) {
	factory, ok := Index["weigher_failover_host"]
	if !ok {
		t.Fatal("expected weigher_failover_host to be registered in Index")
	}
	if _, ok := factory().(*WeigherFailoverHost); !ok {
		t.Fatal("expected factory to return a *WeigherFailoverHost")
	}
}
