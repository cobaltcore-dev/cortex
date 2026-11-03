// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"testing"

	"github.com/go-logr/logr"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/scheduling"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func failoverHostReservation(targetHost, statusHost string) *v1alpha1.Reservation {
	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "failover-host-" + targetHost},
		Spec: v1alpha1.ReservationSpec{
			Type:                    v1alpha1.ReservationTypeFailoverHost,
			TargetHost:              targetHost,
			FailoverHostReservation: &v1alpha1.FailoverHostReservationSpec{},
		},
		Status: v1alpha1.ReservationStatus{Host: statusHost},
	}
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

func requestWithHosts(hints map[string]any, hosts ...string) api.ExternalSchedulerRequest {
	hvHosts := make([]api.ExternalSchedulerHost, len(hosts))
	for i, h := range hosts {
		hvHosts[i] = api.ExternalSchedulerHost{ComputeHost: h}
	}
	return api.ExternalSchedulerRequest{
		Hosts: hvHosts,
		Spec:  api.NovaObject[api.NovaSpec]{Data: api.NovaSpec{SchedulerHints: hints}},
	}
}

func TestFilterNoFailoverHost_Run(t *testing.T) {
	scheme := buildTestScheme(t)

	tests := []struct {
		name          string
		opts          FilterNoFailoverHostOpts
		reservations  []client.Object
		request       api.ExternalSchedulerRequest
		expectedHosts []string
	}{
		{
			name:          "disabled - all hosts pass even with reservations",
			opts:          FilterNoFailoverHostOpts{Enabled: false},
			reservations:  []client.Object{failoverHostReservation("host2", "")},
			request:       requestWithHosts(nil, "host1", "host2", "host3"),
			expectedHosts: []string{"host1", "host2", "host3"},
		},
		{
			name:          "enabled - blocks reserved target host",
			opts:          FilterNoFailoverHostOpts{Enabled: true},
			reservations:  []client.Object{failoverHostReservation("host2", "")},
			request:       requestWithHosts(nil, "host1", "host2", "host3"),
			expectedHosts: []string{"host1", "host3"},
		},
		{
			name:          "enabled - blocks both target and status host",
			opts:          FilterNoFailoverHostOpts{Enabled: true},
			reservations:  []client.Object{failoverHostReservation("host3", "host4")},
			request:       requestWithHosts(nil, "host1", "host2", "host3", "host4"),
			expectedHosts: []string{"host1", "host2"},
		},
		{
			name:          "enabled - ignores non-failover-host reservations",
			opts:          FilterNoFailoverHostOpts{Enabled: true},
			reservations:  []client.Object{committedResourceReservationOn("host2")},
			request:       requestWithHosts(nil, "host1", "host2", "host3"),
			expectedHosts: []string{"host1", "host2", "host3"},
		},
		{
			name:          "evacuation request - leaves hosts untouched for the matcher",
			opts:          FilterNoFailoverHostOpts{Enabled: true},
			reservations:  []client.Object{failoverHostReservation("host2", "")},
			request:       requestWithHosts(map[string]any{"_nova_check_type": "evacuate"}, "host1", "host2", "host3"),
			expectedHosts: []string{"host1", "host2", "host3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterNoFailoverHost{}
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

func TestFilterNoFailoverHost_CallTimeIgnoredReservationTypes(t *testing.T) {
	scheme := buildTestScheme(t)
	step := &FilterNoFailoverHost{}
	step.Options = FilterNoFailoverHostOpts{Enabled: true}
	step.Client = fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(failoverHostReservation("host2", "")).Build()

	request := requestWithHosts(nil, "host1", "host2", "host3")
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

func TestFilterNoFailoverHost_Index(t *testing.T) {
	factory, ok := Index["filter_no_failover_host"]
	if !ok {
		t.Fatal("expected filter_no_failover_host to be registered in Index")
	}
	if _, ok := factory().(*FilterNoFailoverHost); !ok {
		t.Fatal("expected factory to return a *FilterNoFailoverHost")
	}
}
