// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/placement"
	"github.com/cobaltcore-dev/cortex/pkg/db"

	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/nova"

	testlibDB "github.com/cobaltcore-dev/cortex/pkg/db/testing"
)

func TestFilterExternalCustomerStep_Run(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	// Create dependency tables
	err := testDB.CreateTable(
		testDB.AddTable(nova.Hypervisor{}),
		testDB.AddTable(placement.Trait{}),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock hypervisor data
	hypervisors := []any{
		&nova.Hypervisor{ID: "hv1", Hostname: "hypervisor1", State: "up", Status: "enabled", HypervisorType: "QEMU", HypervisorVersion: 2008000, HostIP: "192.168.1.1", ServiceID: "svc1", ServiceHost: "host1", VCPUs: 16, MemoryMB: 32768, LocalGB: 1000, VCPUsUsed: 4, MemoryMBUsed: 8192, LocalGBUsed: 100, FreeRAMMB: 24576, FreeDiskGB: 900, CurrentWorkload: 0, RunningVMs: 2, DiskAvailableLeast: &[]int{900}[0], CPUInfo: "{}"},
		&nova.Hypervisor{ID: "hv2", Hostname: "hypervisor2", State: "up", Status: "enabled", HypervisorType: "QEMU", HypervisorVersion: 2008000, HostIP: "192.168.1.2", ServiceID: "svc2", ServiceHost: "host2", VCPUs: 16, MemoryMB: 32768, LocalGB: 1000, VCPUsUsed: 4, MemoryMBUsed: 8192, LocalGBUsed: 100, FreeRAMMB: 24576, FreeDiskGB: 900, CurrentWorkload: 0, RunningVMs: 2, DiskAvailableLeast: &[]int{900}[0], CPUInfo: "{}"},
		&nova.Hypervisor{ID: "hv3", Hostname: "hypervisor3", State: "up", Status: "enabled", HypervisorType: "QEMU", HypervisorVersion: 2008000, HostIP: "192.168.1.3", ServiceID: "svc3", ServiceHost: "host3", VCPUs: 16, MemoryMB: 32768, LocalGB: 1000, VCPUsUsed: 4, MemoryMBUsed: 8192, LocalGBUsed: 100, FreeRAMMB: 24576, FreeDiskGB: 900, CurrentWorkload: 0, RunningVMs: 2, DiskAvailableLeast: &[]int{900}[0], CPUInfo: "{}"},
		&nova.Hypervisor{ID: "hv4", Hostname: "hypervisor4", State: "up", Status: "enabled", HypervisorType: "QEMU", HypervisorVersion: 2008000, HostIP: "192.168.1.4", ServiceID: "svc4", ServiceHost: "host4", VCPUs: 16, MemoryMB: 32768, LocalGB: 1000, VCPUsUsed: 4, MemoryMBUsed: 8192, LocalGBUsed: 100, FreeRAMMB: 24576, FreeDiskGB: 900, CurrentWorkload: 0, RunningVMs: 2, DiskAvailableLeast: &[]int{900}[0], CPUInfo: "{}"},
	}
	if err := testDB.Insert(hypervisors...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock trait data - host1 and host3 support external customers
	traits := []any{
		&placement.Trait{ResourceProviderUUID: "hv1", Name: "CUSTOM_EXTERNAL_CUSTOMER_SUPPORTED", ResourceProviderGeneration: 1},
		&placement.Trait{ResourceProviderUUID: "hv2", Name: "COMPUTE_STATUS_ENABLED", ResourceProviderGeneration: 1},
		&placement.Trait{ResourceProviderUUID: "hv3", Name: "CUSTOM_EXTERNAL_CUSTOMER_SUPPORTED", ResourceProviderGeneration: 1},
		&placement.Trait{ResourceProviderUUID: "hv4", Name: "COMPUTE_STATUS_ENABLED", ResourceProviderGeneration: 1},
	}
	if err := testDB.Insert(traits...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name          string
		request       api.ExternalSchedulerRequest
		opts          FilterExternalCustomerStepOpts
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name: "External customer domain - filter out external customer hosts",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"domain_name": "external-customer-corp.com",
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
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"external-customer-"},
				CustomerIgnoredDomainNames: []string{},
			},
			expectedHosts: []string{"host2", "host4"}, // Hosts without external customer support
			filteredHosts: []string{"host1", "host3"}, // Hosts with external customer support are filtered out
		},
		{
			name: "Internal domain - no filtering",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"domain_name": "internal.company.com",
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
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"external-customer-"},
				CustomerIgnoredDomainNames: []string{},
			},
			expectedHosts: []string{"host1", "host2", "host3", "host4"},
			filteredHosts: []string{},
		},
		{
			name: "Ignored external customer domain - no filtering",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"domain_name": "external-customer-ignored.com",
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
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"external-customer-"},
				CustomerIgnoredDomainNames: []string{"external-customer-ignored.com"},
			},
			expectedHosts: []string{"host1", "host2", "host3", "host4"},
			filteredHosts: []string{},
		},
		{
			name: "Multiple domain prefixes",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"domain_name": "partner-company.com",
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
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"external-customer-", "partner-"},
				CustomerIgnoredDomainNames: []string{},
			},
			expectedHosts: []string{"host2", "host4"},
			filteredHosts: []string{"host1", "host3"},
		},
		{
			name: "Domain hint as array",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"domain_name": []any{"external-customer-test.com"},
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
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"external-customer-"},
				CustomerIgnoredDomainNames: []string{},
			},
			expectedHosts: []string{"host2", "host4"},
			filteredHosts: []string{"host1", "host3"},
		},
		{
			name: "No domain hint",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"external-customer-"},
				CustomerIgnoredDomainNames: []string{},
			},
			expectedHosts: []string{}, // Should return error, but we expect empty result
			filteredHosts: []string{"host1", "host2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterExternalCustomerStep{}
			step.DB = &testDB
			step.Options = tt.opts
			result, err := step.Run(slog.Default(), tt.request)

			// For the "No domain hint" test case, we expect an error
			if tt.name == "No domain hint" {
				if err == nil {
					t.Errorf("expected error for missing domain hint, got nil")
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
			name: "Valid options",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"external-customer-"},
				CustomerIgnoredDomainNames: []string{},
			},
			expectError: false,
		},
		{
			name: "Multiple prefixes",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{"external-customer-", "partner-"},
				CustomerIgnoredDomainNames: []string{"ignored.com"},
			},
			expectError: false,
		},
		{
			name: "Empty prefixes",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: []string{},
				CustomerIgnoredDomainNames: []string{},
			},
			expectError: true,
		},
		{
			name: "Nil prefixes",
			opts: FilterExternalCustomerStepOpts{
				CustomerDomainNamePrefixes: nil,
				CustomerIgnoredDomainNames: []string{},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()
			if tt.expectError && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}
