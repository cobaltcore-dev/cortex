// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"github.com/go-logr/logr"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFilterExternalCustomerStep_Run(t *testing.T) {
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
				Traits: []string{"CUSTOM_EXTERNAL_CUSTOMER_EXCLUSIVE"},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host2",
			},
			Status: hv1.HypervisorStatus{
				Traits: []string{"CUSTOM_EXTERNAL_CUSTOMER_EXCLUSIVE", "SOME_OTHER_TRAIT"},
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
				Traits: []string{"CUSTOM_EXTERNAL_CUSTOMER_EXCLUSIVE"},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host-custom-trait",
			},
			Spec: hv1.HypervisorSpec{
				CustomTraits: []string{"CUSTOM_EXTERNAL_CUSTOMER_EXCLUSIVE"},
			},
			// Status.Traits intentionally empty — trait comes solely from Spec.CustomTraits.
		},
	}

	tests := []struct {
		name          string
		opts          FilterExternalCustomerStepOpts
		request       compute.PlacementRequest
		expectedHosts []string
		filteredHosts []string
		expectError   bool
	}{
		{
			name: "External customer domain matches prefix - filter to supported hosts",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
				CustomerAllowedDomainNames: []string{"ext-*"},
			},
			request: compute.PlacementRequest{
				DomainName: "ext-customer1",
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
			name: "Domain does not match external customer prefix - only non-exclusive hosts pass",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
			},
			request: compute.PlacementRequest{
				DomainName: "internal-customer",
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host3", "host4"},
			filteredHosts: []string{"host1", "host2"},
		},
		{
			name: "Domain does not match external customer prefix (with allow-list) - allow-list irrelevant, only non-exclusive hosts pass",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
				CustomerAllowedDomainNames: []string{"ext-*"},
			},
			request: compute.PlacementRequest{
				DomainName: "internal-customer",
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host3", "host4"},
			filteredHosts: []string{"host1", "host2"},
		},
		{
			name: "External customer domain not on allow-list - placement blocked",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
				CustomerAllowedDomainNames: []string{"ext-allowed"},
			},
			request: compute.PlacementRequest{
				DomainName: "ext-customer1",
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
			name: "Empty allow-list blocks all external customer domains (fail-closed)",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
			},
			request: compute.PlacementRequest{
				DomainName: "ext-customer1",
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
			name: "Exact allow-list entry proceeds normally",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
				CustomerAllowedDomainNames: []string{"ext-customer1"},
			},
			request: compute.PlacementRequest{
				DomainName: "ext-customer1",
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host3"},
		},
		{
			name: "Multiple domain prefixes - matches second prefix",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"external-", "ext-", "customer-"},
				CustomerAllowedDomainNames: []string{"customer-*"},
			},
			request: compute.PlacementRequest{
				DomainName: "customer-abc",
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host3", "host4"},
		},
		{
			name: "Domain in ignored list - treated as regular tenant, kept off exclusive hosts",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
				CustomerIgnoredDomainNames: []string{"ext-special-domain"},
				CustomerAllowedDomainNames: []string{"ext-*"},
			},
			request: compute.PlacementRequest{
				DomainName: "ext-special-domain",
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1", "host2"},
		},
		{
			name: "Only hosts with trait should remain for external customer",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
				CustomerAllowedDomainNames: []string{"ext-*"},
			},
			request: compute.PlacementRequest{
				DomainName: "ext-customer2",
				Hosts: []compute.Host{
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
				CustomerAllowedDomainNames: []string{"ext-*"},
			},
			request: compute.PlacementRequest{
				DomainName: "ext-customer3",
				Hosts: []compute.Host{
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
				CustomerAllowedDomainNames: []string{"ext-*"},
			},
			request: compute.PlacementRequest{
				DomainName: "ext-customer",
				Hosts:      []compute.Host{},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
		},
		{
			name: "Domain name as list - uses first element",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
				CustomerAllowedDomainNames: []string{"ext-*"},
			},
			request: compute.PlacementRequest{
				DomainName: "ext-customer",
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host3"},
		},
		{
			name: "Missing domain_name in scheduler hints - regular tenant, kept off exclusive hosts",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
			},
			request: compute.PlacementRequest{
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1"},
		},
		{
			name: "Missing domain_name in scheduler hints (with allow-list) - allow-list irrelevant, kept off exclusive hosts",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
				CustomerAllowedDomainNames: []string{"ext-*"},
			},
			request: compute.PlacementRequest{
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1"},
		},
		{
			name: "Nil scheduler hints - regular tenant, kept off exclusive hosts",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
			},
			request: compute.PlacementRequest{
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1", "host2"},
		},
		{
			name: "Nil scheduler hints (with allow-list) - allow-list irrelevant, kept off exclusive hosts",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
				CustomerAllowedDomainNames: []string{"ext-*"},
			},
			request: compute.PlacementRequest{
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1", "host2"},
		},
		{
			name: "Case sensitive prefix matching - EXT- is a regular tenant",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
			},
			request: compute.PlacementRequest{
				DomainName: "EXT-customer",
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1", "host2"},
		},
		{
			name: "Case sensitive prefix matching (with allow-list) - EXT- is a regular tenant, allow-list irrelevant",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
				CustomerAllowedDomainNames: []string{"ext-*"},
			},
			request: compute.PlacementRequest{
				DomainName: "EXT-customer",
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1", "host2"},
		},
		{
			name: "Exact prefix match",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext"},
				CustomerAllowedDomainNames: []string{"ext"},
			},
			request: compute.PlacementRequest{
				DomainName: "ext",
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host3"},
		},
		{
			name: "Multiple ignored domains - treated as regular tenant",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
				CustomerIgnoredDomainNames: []string{"ext-test", "ext-dev", "ext-staging"},
			},
			request: compute.PlacementRequest{
				DomainName: "ext-dev",
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1"},
		},
		{
			name: "Multiple ignored domains (with allow-list) - ignored domain is a regular tenant, allow-list irrelevant",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
				CustomerIgnoredDomainNames: []string{"ext-test", "ext-dev", "ext-staging"},
				CustomerAllowedDomainNames: []string{"ext-*"},
			},
			request: compute.PlacementRequest{
				DomainName: "ext-dev",
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1"},
		},
		{
			name: "ReserveForFailoverIntent for allow-listed external customer - confined to exclusive hosts",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
				CustomerAllowedDomainNames: []string{"ext-*"},
			},
			request: compute.PlacementRequest{
				Intent:     api.ReserveForFailoverIntent,
				DomainName: "ext-customer1",
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host3"},
		},
		{
			name: "ReuseFailoverReservationIntent for non-allow-listed external customer - blocked",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
			},
			request: compute.PlacementRequest{
				Intent:     api.ReuseFailoverReservationIntent,
				DomainName: "ext-customer1",
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1", "host3"},
		},
		{
			name: "CapacityProbeIntent (no domain) - excludes exclusive hosts",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
			},
			request: compute.PlacementRequest{
				Intent: api.CapacityProbeIntent,
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1"},
		},
		{
			name: "CapacityProbeIntent (no domain, with allow-list) - allow-list irrelevant, excludes exclusive hosts",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
				CustomerAllowedDomainNames: []string{"ext-*"},
			},
			request: compute.PlacementRequest{
				Intent: api.CapacityProbeIntent,
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1"},
		},
		{
			name: "ReserveForCommittedResourceIntent - domain filter applies (no domain_name hint → only non-exclusive hosts pass)",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
			},
			request: compute.PlacementRequest{
				Intent: api.ReserveForCommittedResourceIntent,
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1"},
		},
		{
			name: "ReserveForCommittedResourceIntent (with allow-list) - no domain_name hint, allow-list irrelevant",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
				CustomerAllowedDomainNames: []string{"ext-*"},
			},
			request: compute.PlacementRequest{
				Intent: api.ReserveForCommittedResourceIntent,
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1"},
		},
		{
			name: "ReserveForCommittedResourceIntent with external customer domain - filter applies",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
				CustomerAllowedDomainNames: []string{"ext-*"},
			},
			request: compute.PlacementRequest{
				Intent:     api.ReserveForCommittedResourceIntent,
				DomainName: "ext-customer1",
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host3", "host4"},
		},
		{
			name: "Trait from Spec.CustomTraits (not Status.Traits) grants host inclusion",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
				CustomerAllowedDomainNames: []string{"ext-*"},
			},
			request: compute.PlacementRequest{
				DomainName: "ext-customer1",
				Hosts: []compute.Host{
					{ComputeHost: "host-custom-trait"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host-custom-trait"},
			filteredHosts: []string{"host4"},
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

			result, err := step.Run(logr.Discard(), tt.request)
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
			name: "Valid options with allow-list patterns",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
				CustomerAllowedDomainNames: []string{"ext-*", "ext-exact"},
			},
			expectError: false,
		},
		{
			name: "Invalid - malformed allow-list pattern",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"ext-"},
				CustomerAllowedDomainNames: []string{"ext-[", "ext-ok"},
			},
			expectError: true,
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

func TestFilterExternalCustomerStep_FailoverIntentsAppliesDomainRules(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add hv1 to scheme: %v", err)
	}
	// host1 is a regular host, host2 carries the exclusive trait.
	objects := []client.Object{
		&hv1.Hypervisor{ObjectMeta: v1.ObjectMeta{Name: "host1"}},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{Name: "host2"},
			Status:     hv1.HypervisorStatus{Traits: []string{"CUSTOM_EXTERNAL_CUSTOMER_EXCLUSIVE"}},
		},
	}
	step := &FilterExternalCustomerStep{}
	step.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	step.Options = FilterExternalCustomerStepOpts{
		CustomerDomainNamePrefixes: []string{"iaas-"},
		CustomerAllowedDomainNames: []string{"iaas-*"},
	}

	// Failover reservation intents carry the VM's domain and must obey the same
	// exclusive-host rules as a real placement: an allow-listed external customer
	// may only reserve on exclusive hosts.
	for _, intent := range []string{"reserve_for_failover", "reuse_failover_reservation"} {
		t.Run(intent, func(t *testing.T) {
			request := compute.PlacementRequest{
				Intent:     v1alpha1.SchedulingIntent(intent),
				DomainName: "iaas-customer",
				Hosts:      []compute.Host{{ComputeHost: "host1"}, {ComputeHost: "host2"}},
			}
			result, err := step.Run(logr.Discard(), request)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if _, ok := result.Activations["host2"]; !ok || len(result.Activations) != 1 {
				t.Errorf("expected only exclusive host2 to pass, got %v", result.Activations)
			}
		})
	}
}

func TestFilterExternalCustomerStep_matchesExternalCustomerPrefix(t *testing.T) {
	tests := []struct {
		name     string
		opts     FilterExternalCustomerStepOpts
		domain   string
		expected bool
	}{
		{
			name:     "empty domain never matches",
			opts:     FilterExternalCustomerStepOpts{CustomerDomainNamePrefixes: []string{"iaas-"}},
			domain:   "",
			expected: false,
		},
		{
			name:     "matches single prefix",
			opts:     FilterExternalCustomerStepOpts{CustomerDomainNamePrefixes: []string{"iaas-"}},
			domain:   "iaas-customer",
			expected: true,
		},
		{
			name:     "does not match any prefix",
			opts:     FilterExternalCustomerStepOpts{CustomerDomainNamePrefixes: []string{"iaas-"}},
			domain:   "internal-corp",
			expected: false,
		},
		{
			name:     "matches second of several prefixes",
			opts:     FilterExternalCustomerStepOpts{CustomerDomainNamePrefixes: []string{"external-", "iaas-", "customer-"}},
			domain:   "iaas-abc",
			expected: true,
		},
		{
			name:     "prefix matching is case sensitive",
			opts:     FilterExternalCustomerStepOpts{CustomerDomainNamePrefixes: []string{"iaas-"}},
			domain:   "IAAS-customer",
			expected: false,
		},
		{
			name:     "exact prefix equals domain",
			opts:     FilterExternalCustomerStepOpts{CustomerDomainNamePrefixes: []string{"iaas"}},
			domain:   "iaas",
			expected: true,
		},
		{
			name: "ignored domain is not an external customer even if it matches a prefix",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"iaas-"},
				CustomerIgnoredDomainNames: []string{"iaas-internal"},
			},
			domain:   "iaas-internal",
			expected: false,
		},
		{
			name: "non-ignored domain still matches when an ignore list is present",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"iaas-"},
				CustomerIgnoredDomainNames: []string{"iaas-internal"},
			},
			domain:   "iaas-customer",
			expected: true,
		},
		{
			name:     "no prefixes configured never matches",
			opts:     FilterExternalCustomerStepOpts{},
			domain:   "iaas-customer",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterExternalCustomerStep{}
			step.Options = tt.opts
			if got := step.matchesExternalCustomerPrefix(tt.domain); got != tt.expected {
				t.Errorf("matchesExternalCustomerPrefix(%q) = %v, want %v", tt.domain, got, tt.expected)
			}
		})
	}
}

func TestFilterExternalCustomerStep_isAllowed(t *testing.T) {
	tests := []struct {
		name     string
		allowed  []string
		domain   string
		expected bool
	}{
		{
			name:     "empty allow-list blocks everything",
			allowed:  nil,
			domain:   "iaas-customer",
			expected: false,
		},
		{
			name:     "exact match",
			allowed:  []string{"iaas-customer"},
			domain:   "iaas-customer",
			expected: true,
		},
		{
			name:     "exact mismatch",
			allowed:  []string{"iaas-customer"},
			domain:   "iaas-other",
			expected: false,
		},
		{
			name:     "glob suffix wildcard matches",
			allowed:  []string{"iaas-foo-*"},
			domain:   "iaas-foo-prod",
			expected: true,
		},
		{
			name:     "glob suffix wildcard does not match different prefix",
			allowed:  []string{"iaas-foo-*"},
			domain:   "iaas-bar-prod",
			expected: false,
		},
		{
			name:     "match-all wildcard",
			allowed:  []string{"*"},
			domain:   "iaas-anything",
			expected: true,
		},
		{
			name:     "matches second of several patterns",
			allowed:  []string{"iaas-foo-*", "iaas-bar", "iaas-baz-*"},
			domain:   "iaas-baz-dev",
			expected: true,
		},
		{
			name:     "single-segment wildcard does not cross path separator",
			allowed:  []string{"iaas-*"},
			domain:   "iaas-a/b",
			expected: false,
		},
		{
			name:     "malformed pattern is ignored (treated as no match)",
			allowed:  []string{"iaas-["},
			domain:   "iaas-anything",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterExternalCustomerStep{}
			step.Options = FilterExternalCustomerStepOpts{CustomerAllowedDomainNames: tt.allowed}
			if got := step.isAllowed(tt.domain); got != tt.expected {
				t.Errorf("isAllowed(%q) = %v, want %v", tt.domain, got, tt.expected)
			}
		})
	}
}
