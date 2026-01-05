// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFilterExternalCustomerStep_Run(t *testing.T) {
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
				Traits: []string{"CUSTOM_EXTERNAL_CUSTOMER_SUPPORTED"},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host2",
			},
			Status: hv1.HypervisorStatus{
				Traits: []string{"CUSTOM_EXTERNAL_CUSTOMER_SUPPORTED", "SOME_OTHER_TRAIT"},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host3",
			},
			Status: hv1.HypervisorStatus{
				Traits: []string{"SOME_OTHER_TRAIT"},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host4",
			},
			Status: hv1.HypervisorStatus{
				Traits: []string{},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host5",
			},
			Status: hv1.HypervisorStatus{
				Traits: []string{"CUSTOM_EXTERNAL_CUSTOMER_SUPPORTED"},
			},
		},
	}

	tests := []struct {
		name          string
		opts          FilterExternalCustomerStepOpts
		request       api.ExternalSchedulerRequest
		expectedHosts []string
		filteredHosts []string
		expectError   bool
	}{
		{
			name: "External customer domain matches prefix - filter to supported hosts",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"domain_name": "ext-customer1",
						},
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
			name: "Domain does not match external customer prefix - all hosts pass",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"domain_name": "internal-customer",
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3", "host4"},
			filteredHosts: []string{},
		},
		{
			name: "Multiple domain prefixes - matches second prefix",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"external-", "ext-", "customer-"},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"domain_name": "customer-abc",
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host3", "host4"},
		},
		{
			name: "Domain in ignored list - all hosts pass",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
				CustomerIgnoredDomainNames: []string{"ext-special-domain"},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"domain_name": "ext-special-domain",
						},
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
			name: "Only hosts with trait should remain for external customer",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"domain_name": "ext-customer2",
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host5"},
			filteredHosts: []string{},
		},
		{
			name: "No hosts with trait - all filtered",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"domain_name": "ext-customer3",
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host3", "host4"},
		},
		{
			name: "Empty host list",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"domain_name": "ext-customer",
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
		},
		{
			name: "Domain name as list - uses first element",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"domain_name": []any{"ext-customer", "other"},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host3"},
		},
		{
			name: "Missing domain_name in scheduler hints - error",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			expectError: true,
		},
		{
			name: "Nil scheduler hints - error",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: nil,
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			expectError: true,
		},
		{
			name: "Case sensitive prefix matching",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"domain_name": "EXT-customer",
						},
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
			name: "Exact prefix match",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext"},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"domain_name": "ext",
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host3"},
		},
		{
			name: "Multiple ignored domains",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
				CustomerIgnoredDomainNames: []string{"ext-test", "ext-dev", "ext-staging"},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"domain_name": "ext-dev",
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1", "host3"},
			filteredHosts: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterExternalCustomerStep{}
			step.Client = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(hvs...).
				Build()
			step.Options = tt.opts

			result, err := step.Run(slog.Default(), tt.request)
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}
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

func TestFilterExternalCustomerStepOpts_Validate(t *testing.T) {
	tests := []struct {
		name        string
		opts        FilterExternalCustomerStepOpts
		expectError bool
	}{
		{
			name: "Valid options with single prefix",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
			},
			expectError: false,
		},
		{
			name: "Valid options with multiple prefixes",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-", "external-", "customer-"},
			},
			expectError: false,
		},
		{
			name: "Valid options with prefixes and ignored domains",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
				CustomerIgnoredDomainNames: []string{"ext-test"},
			},
			expectError: false,
		},
		{
			name: "Invalid - empty domain name prefixes",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{},
			},
			expectError: true,
		},
		{
			name: "Invalid - nil domain name prefixes",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: nil,
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()
			if tt.expectError && err == nil {
				t.Errorf("expected validation error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no validation error but got: %v", err)
			}
		})
	}
}
