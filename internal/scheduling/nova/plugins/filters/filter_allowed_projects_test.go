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

func TestFilterAllowedProjectsStep_Run(t *testing.T) {
	scheme, err := hv1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hvs := []client.Object{
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host1",
			},
			Spec: hv1.HypervisorSpec{
				AllowedProjects: []string{},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host2",
			},
			Spec: hv1.HypervisorSpec{
				AllowedProjects: []string{"project-a", "project-b"},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host3",
			},
			Spec: hv1.HypervisorSpec{
				AllowedProjects: []string{"project-a"},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host4",
			},
			Spec: hv1.HypervisorSpec{
				AllowedProjects: []string{"project-c"},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host5",
			},
			Spec: hv1.HypervisorSpec{
				AllowedProjects: []string{},
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
			name: "No project ID in request - all hosts pass",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{},
		},
		{
			name: "Project matches allowed projects on host2",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-a",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{},
		},
		{
			name: "Project matches allowed projects on host3 only",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-a",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host2", "host3"},
			filteredHosts: []string{"host4"},
		},
		{
			name: "Project does not match any allowed projects",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-d",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host2", "host3", "host4"},
		},
		{
			name: "Hosts without allowed projects are accessible",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-x",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1", "host5"},
			filteredHosts: []string{},
		},
		{
			name: "Mixed hosts - some with empty allowed projects, some with matching project",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-b",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
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
			name: "All hosts filtered out",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-nonexistent",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host2", "host3", "host4"},
		},
		{
			name: "Empty host list",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-a",
					},
				},
				Hosts: []api.ExternalSchedulerHost{},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
		},
		{
			name: "Project matches on multiple hosts with allowed projects",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-a",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3", "host5"},
			filteredHosts: []string{"host4"},
		},
		{
			name: "Project matches second item in allowed projects list",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-b",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{"host2"},
			filteredHosts: []string{},
		},
		{
			name: "All hosts have empty allowed projects",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "any-project",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1", "host5"},
			filteredHosts: []string{},
		},
		{
			name: "Mixed allowed projects configuration",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-c",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1", "host4", "host5"},
			filteredHosts: []string{"host2", "host3"},
		},
		{
			name: "Case sensitive project matching",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "Project-A",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host2", "host3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterAllowedProjectsStep{}
			step.Client = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(hvs...).
				Build()
			result, err := step.Run(t.Context(), slog.Default(), tt.request)
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
