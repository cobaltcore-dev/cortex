// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"testing"

	"github.com/go-logr/logr"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/scheduling"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// failoverHostReservation builds an Active failover host reservation targeting
// targetHost (with statusHost as its actual host), able to rescue the given
// source hosts. Active is the state in which a reservation can be selected as an
// evacuation target; use notActive() to drop it back to the draining state.
func failoverHostReservation(targetHost, statusHost string, sourceHosts ...string) *v1alpha1.Reservation {
	res := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "failover-host-" + targetHost},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeFailoverHost,
			TargetHost: targetHost,
			FailoverHostReservation: &v1alpha1.FailoverHostReservationSpec{
				SourceHosts: sourceHosts,
			},
		},
		Status: v1alpha1.ReservationStatus{Host: statusHost},
	}
	meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
		Type:   v1alpha1.ConditionTypeFailoverHostActive,
		Status: metav1.ConditionTrue,
		Reason: "HostDrained",
	})
	return res
}

// notActive drops a reservation out of the Active state, back to the draining
// stage (the same as a freshly created reservation): it still blocks ordinary
// VMs but cannot yet be selected for evacuation.
func notActive(res *v1alpha1.Reservation) *v1alpha1.Reservation {
	meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
		Type:   v1alpha1.ConditionTypeFailoverHostActive,
		Status: metav1.ConditionFalse,
		Reason: "Draining",
	})
	return res
}

// inUseBy marks a failover host reservation as in use by the given source host.
func inUseBy(res *v1alpha1.Reservation, sourceHost string) *v1alpha1.Reservation {
	res.Status.FailoverHostReservation = &v1alpha1.FailoverHostReservationStatus{SourceHost: sourceHost}
	meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
		Type:   v1alpha1.ConditionTypeFailoverHostInUse,
		Status: metav1.ConditionTrue,
		Reason: "EvacuationStarted",
	})
	return res
}

func committedResourceReservationOn(targetHost string) *v1alpha1.Reservation {
	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "cr-" + targetHost},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeCommittedResource,
			TargetHost: targetHost,
		},
	}
}

// requestWithHosts builds a typed placement request over the named candidate
// hosts, with the given intent and source host (empty for non-evacuations).
func requestWithHosts(intent v1alpha1.SchedulingIntent, sourceHost string, hosts ...string) compute.PlacementRequest {
	hvHosts := make([]compute.Host, len(hosts))
	for i, h := range hosts {
		hvHosts[i] = compute.Host{ComputeHost: h}
	}
	return compute.PlacementRequest{
		Hosts:      hvHosts,
		Intent:     intent,
		SourceHost: sourceHost,
	}
}

func TestFilterFailoverHost_Run(t *testing.T) {
	scheme := buildTestScheme(t)

	tests := []struct {
		name          string
		opts          FilterFailoverHostOpts
		reservations  []client.Object
		request       compute.PlacementRequest
		expectedHosts []string
	}{
		{
			name:          "disabled - all hosts pass even with reservations",
			opts:          FilterFailoverHostOpts{Enabled: false},
			reservations:  []client.Object{failoverHostReservation("host2", "")},
			request:       requestWithHosts("", "", "host1", "host2", "host3"),
			expectedHosts: []string{"host1", "host2", "host3"},
		},
		{
			name:          "non-evacuation - blocks reserved target host",
			opts:          FilterFailoverHostOpts{Enabled: true},
			reservations:  []client.Object{failoverHostReservation("host2", "")},
			request:       requestWithHosts("", "", "host1", "host2", "host3"),
			expectedHosts: []string{"host1", "host3"},
		},
		{
			name:          "non-evacuation - blocks both target and status host",
			opts:          FilterFailoverHostOpts{Enabled: true},
			reservations:  []client.Object{failoverHostReservation("host3", "host4")},
			request:       requestWithHosts("", "", "host1", "host2", "host3", "host4"),
			expectedHosts: []string{"host1", "host2"},
		},
		{
			name:          "non-evacuation - ignores non-failover-host reservations",
			opts:          FilterFailoverHostOpts{Enabled: true},
			reservations:  []client.Object{committedResourceReservationOn("host2")},
			request:       requestWithHosts("", "", "host1", "host2", "host3"),
			expectedHosts: []string{"host1", "host2", "host3"},
		},
		{
			name: "non-evacuation - blocks the in-use source host as well as the target",
			opts: FilterFailoverHostOpts{Enabled: true},
			// h1 is reserved (target) and in use by source source1; both the reserved
			// host and the source it is draining are kept free of new VMs.
			reservations:  []client.Object{inUseBy(failoverHostReservation("h1", "", "source1"), "source1")},
			request:       requestWithHosts("", "", "h1", "source1", "host3"),
			expectedHosts: []string{"host3"},
		},
		{
			name: "evacuation - keeps the selected target and prunes other failover targets",
			opts: FilterFailoverHostOpts{Enabled: true},
			// h1 and h2 are both reachable from source1; only the name-first (h1)
			// is kept, h2 is pruned to stay free for another failure. h3/h4 ordinary.
			reservations: []client.Object{
				failoverHostReservation("h1", "", "source1"),
				failoverHostReservation("h2", "", "source1"),
			},
			request:       requestWithHosts(api.EvacuateIntent, "source1", "h1", "h2", "h3", "h4"),
			expectedHosts: []string{"h1", "h3", "h4"},
		},
		{
			name: "evacuation - reuses the in-use target for the same source",
			opts: FilterFailoverHostOpts{Enabled: true},
			// h2 is in use by source1, so it is the selected target even though h1
			// sorts first; h1 is pruned.
			reservations: []client.Object{
				failoverHostReservation("h1", "", "source1"),
				inUseBy(failoverHostReservation("h2", "", "source1"), "source1"),
			},
			request:       requestWithHosts(api.EvacuateIntent, "source1", "h1", "h2", "h3"),
			expectedHosts: []string{"h2", "h3"},
		},
		{
			name: "evacuation - never keeps a target in use by another source",
			opts: FilterFailoverHostOpts{Enabled: true},
			// h1 is in use by source2, so source1 selects the free h2; both reserved
			// hosts except the selected h2 are pruned.
			reservations: []client.Object{
				inUseBy(failoverHostReservation("h1", "", "source1", "source2"), "source2"),
				failoverHostReservation("h2", "", "source1", "source2"),
			},
			request:       requestWithHosts(api.EvacuateIntent, "source1", "h1", "h2", "h3"),
			expectedHosts: []string{"h2", "h3"},
		},
		{
			name: "evacuation - no matching reservation removes all failover hosts",
			opts: FilterFailoverHostOpts{Enabled: true},
			reservations: []client.Object{
				failoverHostReservation("h1", "", "otherSource"),
			},
			request:       requestWithHosts(api.EvacuateIntent, "source1", "h1", "h2", "h3"),
			expectedHosts: []string{"h2", "h3"},
		},
		{
			name:          "evacuation - no source hint removes all failover hosts",
			opts:          FilterFailoverHostOpts{Enabled: true},
			reservations:  []client.Object{failoverHostReservation("h1", "", "source1")},
			request:       requestWithHosts(api.EvacuateIntent, "", "h1", "h2", "h3"),
			expectedHosts: []string{"h2", "h3"},
		},
		{
			name: "evacuation - a not-yet-active target is still blocked",
			opts: FilterFailoverHostOpts{Enabled: true},
			// h1 is freshly created (not Active yet), so it cannot be a landing
			// zone and is removed like any other reserved host, even though the
			// source could otherwise evacuate onto it.
			reservations:  []client.Object{notActive(failoverHostReservation("h1", "", "source1"))},
			request:       requestWithHosts(api.EvacuateIntent, "source1", "h1", "h2", "h3"),
			expectedHosts: []string{"h2", "h3"},
		},
		{
			name: "evacuation - selects the active target and prunes a not-yet-active one",
			opts: FilterFailoverHostOpts{Enabled: true},
			// h1 sorts first but is not Active yet; h2 is Active, so h2 is the
			// selected target and h1 is pruned as an unusable reserved host.
			reservations: []client.Object{
				notActive(failoverHostReservation("h1", "", "source1")),
				failoverHostReservation("h2", "", "source1"),
			},
			request:       requestWithHosts(api.EvacuateIntent, "source1", "h1", "h2", "h3"),
			expectedHosts: []string{"h2", "h3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterFailoverHost{}
			step.Options = tt.opts
			step.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.reservations...).Build()

			result, err := step.Run(logr.Discard(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if len(result.Activations) != len(tt.expectedHosts) {
				t.Errorf("expected %d hosts, got %d: %v", len(tt.expectedHosts), len(result.Activations), result.Activations)
			}
			for _, host := range tt.expectedHosts {
				if _, ok := result.Activations[host]; !ok {
					t.Errorf("expected host %s to be present", host)
				}
			}
		})
	}
}

func TestFilterFailoverHost_CallTimeIgnoredReservationTypes(t *testing.T) {
	scheme := buildTestScheme(t)
	step := &FilterFailoverHost{}
	step.Options = FilterFailoverHostOpts{Enabled: true}
	step.Client = fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(failoverHostReservation("host2", "")).Build()

	request := requestWithHosts("", "", "host1", "host2", "host3")
	request.Options = scheduling.Options{
		IgnoredReservationTypes: []v1alpha1.ReservationType{v1alpha1.ReservationTypeFailoverHost},
	}
	result, err := step.Run(logr.Discard(), request)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, ok := result.Activations["host2"]; !ok {
		t.Error("expected host2 to survive when failover host reservations are ignored at call time")
	}
}

func TestFilterFailoverHost_Index(t *testing.T) {
	factory, ok := Index["filter_failover_host"]
	if !ok {
		t.Fatal("expected filter_failover_host to be registered in Index")
	}
	if _, ok := factory().(*FilterFailoverHost); !ok {
		t.Fatal("expected factory to return a *FilterFailoverHost")
	}
}

func TestFilterFailoverHost_OptsValidate(t *testing.T) {
	if err := (FilterFailoverHostOpts{}).Validate(); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if err := (FilterFailoverHostOpts{Enabled: true}).Validate(); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}
