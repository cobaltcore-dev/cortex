// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/placement"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestFilterExternalCustomerStep_Run(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
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
	_, err = testDB.Exec(`
		INSERT INTO openstack_hypervisors (id, hostname, state, status, hypervisor_type, hypervisor_version, host_ip, service_id, service_host, service_disabled_reason, vcpus, memory_mb, local_gb, vcpus_used, memory_mb_used, local_gb_used, free_ram_mb, free_disk_gb, current_workload, running_vms, disk_available_least, cpu_info)
		VALUES
			('hv1', 'hypervisor1', 'up', 'enabled', 'QEMU', 2008000, '192.168.1.1', 'svc1', 'host1', NULL, 16, 32768, 1000, 4, 8192, 100, 24576, 900, 0, 2, 900, '{}'),
			('hv2', 'hypervisor2', 'up', 'enabled', 'QEMU', 2008000, '192.168.1.2', 'svc2', 'host2', NULL, 16, 32768, 1000, 4, 8192, 100, 24576, 900, 0, 2, 900, '{}'),
			('hv3', 'hypervisor3', 'up', 'enabled', 'QEMU', 2008000, '192.168.1.3', 'svc3', 'host3', NULL, 16, 32768, 1000, 4, 8192, 100, 24576, 900, 0, 2, 900, '{}'),
			('hv4', 'hypervisor4', 'up', 'enabled', 'QEMU', 2008000, '192.168.1.4', 'svc4', 'host4', NULL, 16, 32768, 1000, 4, 8192, 100, 24576, 900, 0, 2, 900, '{}')
	`)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock trait data - host1 and host3 support external customers
	_, err = testDB.Exec(`
		INSERT INTO openstack_resource_provider_traits (resource_provider_uuid, name, resource_provider_generation)
		VALUES
			('hv1', 'CUSTOM_EXTERNAL_CUSTOMER_SUPPORTED', 1),
			('hv2', 'COMPUTE_STATUS_ENABLED', 1),
			('hv3', 'CUSTOM_EXTERNAL_CUSTOMER_SUPPORTED', 1),
			('hv4', 'COMPUTE_STATUS_ENABLED', 1)
	`)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name          string
		request       api.ExternalSchedulerRequest
		opts          string
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
			opts: `{
				"domainNamePrefixes": ["external-customer-"],
				"ignoredDomainNames": []
			}`,
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
			opts: `{
				"domainNamePrefixes": ["external-customer-"],
				"ignoredDomainNames": []
			}`,
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
			opts: `{
				"domainNamePrefixes": ["external-customer-"],
				"ignoredDomainNames": ["external-customer-ignored.com"]
			}`,
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
			opts: `{
				"domainNamePrefixes": ["external-customer-", "partner-"],
				"ignoredDomainNames": []
			}`,
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
			opts: `{
				"domainNamePrefixes": ["external-customer-"],
				"ignoredDomainNames": []
			}`,
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
			opts: `{
				"domainNamePrefixes": ["external-customer-"],
				"ignoredDomainNames": []
			}`,
			expectedHosts: []string{}, // Should return error, but we expect empty result
			filteredHosts: []string{"host1", "host2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterExternalCustomerStep{}
			if err := step.Init("", testDB, conf.NewRawOpts(tt.opts)); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
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
