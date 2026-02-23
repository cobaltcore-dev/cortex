// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFilterStatusConditionsStep_Run(t *testing.T) {
	scheme, err := hv1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hvs := []client.Object{
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host1",
			},
			Status: hv1.HypervisorStatus{
				Conditions: []v1.Condition{
					{
						Type:   hv1.ConditionTypeReady,
						Status: v1.ConditionTrue,
					},
					{
						Type:   hv1.ConditionTypeHypervisorDisabled,
						Status: v1.ConditionFalse,
					},
					{
						Type:   hv1.ConditionTypeTerminating,
						Status: v1.ConditionFalse,
					},
					{
						Type:   hv1.ConditionTypeTainted,
						Status: v1.ConditionFalse,
					},
				},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host2",
			},
			Status: hv1.HypervisorStatus{
				Conditions: []v1.Condition{
					{
						Type:   hv1.ConditionTypeReady,
						Status: v1.ConditionFalse,
					},
					{
						Type:   hv1.ConditionTypeHypervisorDisabled,
						Status: v1.ConditionFalse,
					},
					{
						Type:   hv1.ConditionTypeTerminating,
						Status: v1.ConditionFalse,
					},
					{
						Type:   hv1.ConditionTypeTainted,
						Status: v1.ConditionFalse,
					},
				},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host3",
			},
			Status: hv1.HypervisorStatus{
				Conditions: []v1.Condition{
					{
						Type:   hv1.ConditionTypeReady,
						Status: v1.ConditionTrue,
					},
					{
						Type:   hv1.ConditionTypeHypervisorDisabled,
						Status: v1.ConditionFalse,
					},
					{
						Type:   hv1.ConditionTypeTerminating,
						Status: v1.ConditionTrue,
					},
					{
						Type:   hv1.ConditionTypeTainted,
						Status: v1.ConditionFalse,
					},
				},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host4",
			},
			Status: hv1.HypervisorStatus{
				Conditions: []v1.Condition{
					{
						Type:   hv1.ConditionTypeReady,
						Status: v1.ConditionTrue,
					},
					{
						Type:   hv1.ConditionTypeHypervisorDisabled,
						Status: v1.ConditionFalse,
					},
					{
						Type:   hv1.ConditionTypeTerminating,
						Status: v1.ConditionFalse,
					},
					{
						Type:   hv1.ConditionTypeTainted,
						Status: v1.ConditionTrue,
					},
				},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host5",
			},
			Status: hv1.HypervisorStatus{
				Conditions: []v1.Condition{
					{
						Type:   hv1.ConditionTypeOnboarding,
						Status: v1.ConditionTrue,
					},
					{
						Type:   hv1.ConditionTypeReady,
						Status: v1.ConditionTrue,
					},
					{
						Type:   hv1.ConditionTypeHypervisorDisabled,
						Status: v1.ConditionFalse,
					},
					{
						Type:   hv1.ConditionTypeTerminating,
						Status: v1.ConditionFalse,
					},
					{
						Type:   hv1.ConditionTypeTainted,
						Status: v1.ConditionFalse,
					},
					{
						Type:   hv1.ConditionTypeTraitsUpdated,
						Status: v1.ConditionTrue,
					},
					{
						Type:   hv1.ConditionTypeAggregatesUpdated,
						Status: v1.ConditionFalse,
					},
				},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host6",
			},
			Status: hv1.HypervisorStatus{
				Conditions: []v1.Condition{
					{
						Type:   hv1.ConditionTypeTerminating,
						Status: v1.ConditionFalse,
					},
					{
						Type:   hv1.ConditionTypeTainted,
						Status: v1.ConditionFalse,
					},
				},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host7",
			},
			Status: hv1.HypervisorStatus{
				Conditions: []v1.Condition{},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host8",
			},
			Status: hv1.HypervisorStatus{
				Conditions: []v1.Condition{
					{
						Type:   hv1.ConditionTypeReady,
						Status: v1.ConditionTrue,
					},
					{
						Type:   hv1.ConditionTypeHypervisorDisabled,
						Status: v1.ConditionTrue,
					},
					{
						Type:   hv1.ConditionTypeTerminating,
						Status: v1.ConditionFalse,
					},
					{
						Type:   hv1.ConditionTypeTainted,
						Status: v1.ConditionFalse,
					},
				},
			},
		},
	}

	tests := []struct {
		name          string
		request       api.ExternalSchedulerRequest
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name: "Filter hosts with all conditions met",
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host2", "host3", "host4"},
		},
		{
			name: "Host not ready should be filtered",
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host2"},
		},
		{
			name: "Terminating host should be filtered",
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host3"},
		},
		{
			name: "Tainted host should be filtered",
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host4"},
		},
		{
			name: "Disabled hypervisor should be filtered",
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host8"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host8"},
		},
		{
			name: "Host with optional conditions in any state should pass",
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host5"},
			filteredHosts: []string{},
		},
		{
			name: "Host missing Ready condition should be kept",
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{"host6"},
			filteredHosts: []string{},
		},
		{
			name: "Host with no conditions should be kept",
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host7"},
				},
			},
			expectedHosts: []string{"host7"},
			filteredHosts: []string{},
		},
		{
			name: "Empty host list",
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
		},
		{
			name: "Host not in database",
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host-unknown"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host-unknown"},
		},
		{
			name: "Mixed condition states",
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1", "host5"},
			filteredHosts: []string{"host2", "host3", "host4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterStatusConditionsStep{}
			step.Client = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(hvs...).
				Build()
			result, err := step.Run(slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			// Check expected hosts are present
			for _, host := range tt.expectedHosts {
				if _, ok := result.Activations[host]; !ok {
					t.Errorf("expected host %s to be present in activations", host)
				}
			}

			// Check filtered hosts are not present
			for _, host := range tt.filteredHosts {
				if _, ok := result.Activations[host]; ok {
					t.Errorf("expected host %s to be filtered out", host)
				}
			}

			// Check total count
			if len(result.Activations) != len(tt.expectedHosts) {
				t.Errorf("expected %d hosts, got %d", len(tt.expectedHosts), len(result.Activations))
			}
		})
	}
}
