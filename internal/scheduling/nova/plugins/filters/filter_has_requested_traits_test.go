// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"github.com/go-logr/logr"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFilterHasRequestedTraits_Run(t *testing.T) {
	scheme := runtime.NewScheme()
	err := hv1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hvs := []client.Object{
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host1",
			},
			Status: hv1.HypervisorStatus{
				Traits: []string{"CUSTOM_TRAIT_A", "CUSTOM_TRAIT_B"},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host2",
			},
			Status: hv1.HypervisorStatus{
				Traits: []string{"CUSTOM_TRAIT_A", "CUSTOM_TRAIT_C"},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host3",
			},
			Status: hv1.HypervisorStatus{
				Traits: []string{"CUSTOM_TRAIT_B", "CUSTOM_TRAIT_C"},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host4",
			},
			Status: hv1.HypervisorStatus{
				Traits: []string{"CUSTOM_TRAIT_D"},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host5",
			},
			Status: hv1.HypervisorStatus{
				Traits: []string{},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host6",
			},
			Status: hv1.HypervisorStatus{
				Traits: []string{"CUSTOM_TRAIT_A", "CUSTOM_TRAIT_B", "CUSTOM_TRAIT_C", "CUSTOM_TRAIT_D"},
			},
		},
	}

	tests := []struct {
		name          string
		request       compute.SchedulerRequest
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name: "No traits requested - all hosts pass",
			request: compute.SchedulerRequest{
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{},
		},
		{
			name: "Single required trait - filter hosts with trait",
			request: compute.SchedulerRequest{
				RequiredTraits: []string{"CUSTOM_TRAIT_A"},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{"host3", "host4"},
		},
		{
			name: "Multiple required traits - filter hosts with all traits",
			request: compute.SchedulerRequest{
				RequiredTraits: []string{"CUSTOM_TRAIT_A", "CUSTOM_TRAIT_B"},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{"host1", "host6"},
			filteredHosts: []string{"host2", "host3"},
		},
		{
			name: "Single forbidden trait - filter hosts without trait",
			request: compute.SchedulerRequest{
				ForbiddenTraits: []string{"CUSTOM_TRAIT_A"},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host3", "host4", "host5"},
			filteredHosts: []string{"host1", "host2"},
		},
		{
			name: "Multiple forbidden traits - filter hosts without any of them",
			request: compute.SchedulerRequest{
				ForbiddenTraits: []string{"CUSTOM_TRAIT_A", "CUSTOM_TRAIT_B"},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host4", "host5"},
			filteredHosts: []string{"host1", "host2", "host3"},
		},
		{
			name: "Mixed required and forbidden traits",
			request: compute.SchedulerRequest{
				RequiredTraits:  []string{"CUSTOM_TRAIT_A"},
				ForbiddenTraits: []string{"CUSTOM_TRAIT_D"},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host4"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{"host4", "host6"},
		},
		{
			name: "Required trait that no host has",
			request: compute.SchedulerRequest{
				RequiredTraits: []string{"CUSTOM_TRAIT_NONEXISTENT"},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1", "host2", "host3"},
		},
		{
			name: "Forbidden trait that no host has - all pass",
			request: compute.SchedulerRequest{
				ForbiddenTraits: []string{"CUSTOM_TRAIT_NONEXISTENT"},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{},
		},
		{
			name: "Host with no traits - required trait filters it out",
			request: compute.SchedulerRequest{
				RequiredTraits: []string{"CUSTOM_TRAIT_A"},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host5"},
		},
		{
			name: "Host with no traits - forbidden trait lets it pass",
			request: compute.SchedulerRequest{
				ForbiddenTraits: []string{"CUSTOM_TRAIT_A"},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host5"},
			filteredHosts: []string{"host1"},
		},
		{
			name: "Non-trait extra specs are ignored",
			request: compute.SchedulerRequest{
				RequiredTraits: []string{"CUSTOM_TRAIT_A"},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host3"},
		},
		{
			name: "Invalid trait value (not required or forbidden) - ignored",
			request: compute.SchedulerRequest{
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{},
		},
		{
			name: "Empty host list",
			request: compute.SchedulerRequest{
				RequiredTraits: []string{"CUSTOM_TRAIT_A"},
				Hosts:          []compute.Host{},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
		},
		{
			name: "Host not in database",
			request: compute.SchedulerRequest{
				RequiredTraits: []string{"CUSTOM_TRAIT_A"},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host-unknown"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host-unknown"},
		},
		{
			name: "Complex scenario with many traits",
			request: compute.SchedulerRequest{
				RequiredTraits:  []string{"CUSTOM_TRAIT_A", "CUSTOM_TRAIT_B"},
				ForbiddenTraits: []string{"CUSTOM_TRAIT_D"},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host2", "host3", "host4", "host5", "host6"},
		},
		{
			name: "All hosts match required and forbidden traits",
			request: compute.SchedulerRequest{
				RequiredTraits:  []string{"CUSTOM_TRAIT_A"},
				ForbiddenTraits: []string{"CUSTOM_TRAIT_E"},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterHasRequestedTraits{}
			step.Client = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(hvs...).
				Build()

			result, err := step.Run(logr.Discard(), tt.request)
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
