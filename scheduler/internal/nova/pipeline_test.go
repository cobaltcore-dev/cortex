// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/db"
	delegationAPI "github.com/cobaltcore-dev/cortex/scheduler/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/nova/api"
	"github.com/cobaltcore-dev/cortex/sync/api/objects/openstack/nova"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

// setupTestDBWithHypervisors creates a test database with hypervisor data for premodifier testing.
func setupTestDBWithHypervisors(t *testing.T) db.DB {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}

	// Create table for hypervisors
	err := testDB.CreateTable(
		testDB.AddTable(nova.Hypervisor{}),
	)
	if err != nil {
		t.Fatalf("expected no error creating hypervisors table, got %v", err)
	}

	// Insert mock hypervisor data
	h1Disk := 750
	h2Disk := 1500
	h3SDA := "maintenance"
	h3Disk := 1450
	hypervisors := []any{
		&nova.Hypervisor{
			ID:                    "1",
			Hostname:              "hypervisor-1.example.com",
			State:                 "up",
			Status:                "enabled",
			HypervisorType:        "kvm",
			HypervisorVersion:     2011,
			HostIP:                "192.168.1.10",
			ServiceID:             "service-1",
			ServiceHost:           "nova-compute-1",
			ServiceDisabledReason: nil,
			VCPUs:                 16,
			MemoryMB:              32768,
			LocalGB:               1000,
			VCPUsUsed:             4,
			MemoryMBUsed:          8192,
			LocalGBUsed:           200,
			FreeRAMMB:             24576,
			FreeDiskGB:            800,
			CurrentWorkload:       2,
			RunningVMs:            5,
			DiskAvailableLeast:    &h1Disk,
			CPUInfo:               `{"arch": "x86_64", "model": "Intel"}`,
		},
		&nova.Hypervisor{
			ID:                    "2",
			Hostname:              "hypervisor-2.example.com",
			State:                 "up",
			Status:                "enabled",
			HypervisorType:        "vmware",
			HypervisorVersion:     6070,
			HostIP:                "192.168.1.11",
			ServiceID:             "service-2",
			ServiceHost:           "nova-compute-2",
			ServiceDisabledReason: nil,
			VCPUs:                 32,
			MemoryMB:              65536,
			LocalGB:               2000,
			VCPUsUsed:             8,
			MemoryMBUsed:          16384,
			LocalGBUsed:           400,
			FreeRAMMB:             49152,
			FreeDiskGB:            1600,
			CurrentWorkload:       4,
			RunningVMs:            10,
			DiskAvailableLeast:    &h2Disk,
			CPUInfo:               `{"arch": "x86_64", "model": "AMD"}`,
		},
		&nova.Hypervisor{
			ID:                    "3",
			Hostname:              "hypervisor-3.example.com",
			State:                 "down",
			Status:                "disabled",
			HypervisorType:        "kvm",
			HypervisorVersion:     2011,
			HostIP:                "192.168.1.12",
			ServiceID:             "service-3",
			ServiceHost:           "nova-compute-3",
			ServiceDisabledReason: &h3SDA,
			VCPUs:                 24,
			MemoryMB:              49152,
			LocalGB:               1500,
			VCPUsUsed:             0,
			MemoryMBUsed:          0,
			LocalGBUsed:           0,
			FreeRAMMB:             49152,
			FreeDiskGB:            1500,
			CurrentWorkload:       0,
			RunningVMs:            0,
			DiskAvailableLeast:    &h3Disk,
			CPUInfo:               `{"arch": "x86_64", "model": "Intel"}`,
		},
	}
	if err := testDB.Insert(hypervisors...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if err != nil {
		t.Fatalf("expected no error inserting hypervisor data, got %v", err)
	}

	return testDB
}

func Test_PreselectAllHostsEnabled(t *testing.T) {
	testDB := setupTestDBWithHypervisors(t)
	defer testDB.Close()

	// Create a request with some existing hosts (should be replaced)
	request := &api.PipelineRequest{
		Hosts: []delegationAPI.ExternalSchedulerHost{
			{ComputeHost: "existing-host-1", HypervisorHostname: "existing-hypervisor-1"},
		},
		Weights: map[string]float64{
			"existing-host-1": 1.5,
		},
	}

	pipeline := &novaPipeline{database: testDB, preselectAllHosts: true}
	err := pipeline.modify(request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Request should be modified to include all hypervisors from database
	expectedHosts := []delegationAPI.ExternalSchedulerHost{
		{ComputeHost: "nova-compute-1", HypervisorHostname: "hypervisor-1.example.com"},
		{ComputeHost: "nova-compute-2", HypervisorHostname: "hypervisor-2.example.com"},
		{ComputeHost: "nova-compute-3", HypervisorHostname: "hypervisor-3.example.com"},
	}
	expectedWeights := map[string]float64{
		"nova-compute-1": 0.0,
		"nova-compute-2": 0.0,
		"nova-compute-3": 0.0,
	}

	if len(request.Hosts) != 3 {
		t.Errorf("expected 3 hosts, got %d", len(request.Hosts))
	}
	if len(request.Weights) != 3 {
		t.Errorf("expected 3 weights, got %d", len(request.Weights))
	}

	// Check that all expected hosts are present (order might vary)
	hostMap := make(map[string]string)
	for _, host := range request.Hosts {
		hostMap[host.ComputeHost] = host.HypervisorHostname
	}

	for _, expectedHost := range expectedHosts {
		if hypervisorHostname, exists := hostMap[expectedHost.ComputeHost]; !exists {
			t.Errorf("expected host %s not found", expectedHost.ComputeHost)
		} else if hypervisorHostname != expectedHost.HypervisorHostname {
			t.Errorf("host %s has hypervisor hostname %s, want %s",
				expectedHost.ComputeHost, hypervisorHostname, expectedHost.HypervisorHostname)
		}
	}

	// Check weights
	for computeHost, expectedWeight := range expectedWeights {
		if weight, exists := request.Weights[computeHost]; !exists {
			t.Errorf("expected weight for host %s not found", computeHost)
		} else if weight != expectedWeight {
			t.Errorf("weight for host %s = %f, want %f", computeHost, weight, expectedWeight)
		}
	}
}

func TestPremodifier_ModifyRequest_PreselectAllHostsEnabled_EmptyRequest(t *testing.T) {
	testDB := setupTestDBWithHypervisors(t)
	defer testDB.Close()

	// Create an empty request
	request := &api.PipelineRequest{
		Hosts:   []delegationAPI.ExternalSchedulerHost{},
		Weights: map[string]float64{},
	}

	pipeline := &novaPipeline{database: testDB, preselectAllHosts: true}
	if err := pipeline.modify(request); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should populate with all hypervisors
	if len(request.Hosts) != 3 {
		t.Errorf("expected 3 hosts, got %d", len(request.Hosts))
	}
	if len(request.Weights) != 3 {
		t.Errorf("expected 3 weights, got %d", len(request.Weights))
	}

	// Verify all weights are 0.0
	for _, weight := range request.Weights {
		if weight != 0.0 {
			t.Errorf("expected weight 0.0, got %f", weight)
		}
	}
}

func TestPremodifier_ModifyRequest_NoHypervisors(t *testing.T) {
	// Create empty database
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()

	// Create table but don't insert any data
	err := testDB.CreateTable(
		testDB.AddTable(nova.Hypervisor{}),
	)
	if err != nil {
		t.Fatalf("expected no error creating hypervisors table, got %v", err)
	}

	request := &api.PipelineRequest{
		Hosts:   []delegationAPI.ExternalSchedulerHost{},
		Weights: map[string]float64{},
	}

	pipeline := &novaPipeline{database: testDB, preselectAllHosts: true}
	err = pipeline.modify(request)
	if err == nil {
		t.Fatal("expected error when no hypervisors found, got nil")
	}
	if err.Error() != "no hypervisors found" {
		t.Errorf("expected error 'no hypervisors found', got %v", err)
	}
}

func TestPremodifier_ModifyRequest_DatabaseError(t *testing.T) {
	// Create database but don't create the hypervisors table
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()

	request := &api.PipelineRequest{
		Hosts:   []delegationAPI.ExternalSchedulerHost{},
		Weights: map[string]float64{},
	}

	pipeline := &novaPipeline{database: testDB, preselectAllHosts: true}
	if err := pipeline.modify(request); err == nil {
		t.Fatal("expected database error, got nil")
	}
	// The exact error message will depend on the database implementation,
	// but it should be a database-related error
}

func TestPremodifier_ModifyRequest_PreservesOtherFields(t *testing.T) {
	testDB := setupTestDBWithHypervisors(t)
	defer testDB.Close()

	// Create a request with various fields set
	originalRequest := &api.PipelineRequest{
		Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
			Data: delegationAPI.NovaSpec{
				ProjectID:    "test-project",
				UserID:       "test-user",
				InstanceUUID: "test-instance-uuid",
			},
		},
		Context: delegationAPI.NovaRequestContext{
			UserID:    "test-user",
			ProjectID: "test-project",
			RequestID: "test-request-id",
		},
		Rebuild: true,
		Resize:  false,
		Live:    true,
		VMware:  false,
		Hosts: []delegationAPI.ExternalSchedulerHost{
			{ComputeHost: "original-host", HypervisorHostname: "original-hypervisor"},
		},
		Weights: map[string]float64{
			"original-host": 2.5,
		},
	}

	pipeline := &novaPipeline{database: testDB, preselectAllHosts: true}
	if err := pipeline.modify(originalRequest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify that non-host/weight fields are preserved
	if originalRequest.Spec.Data.ProjectID != "test-project" {
		t.Errorf("spec.data.project_id = %s, want test-project", originalRequest.Spec.Data.ProjectID)
	}
	if originalRequest.Context.UserID != "test-user" {
		t.Errorf("context.user_id = %s, want test-user", originalRequest.Context.UserID)
	}
	if !originalRequest.Rebuild {
		t.Error("rebuild should be true")
	}
	if originalRequest.Resize {
		t.Error("resize should be false")
	}
	if !originalRequest.Live {
		t.Error("live should be true")
	}
	if originalRequest.VMware {
		t.Error("vmware should be false")
	}

	// Verify that hosts and weights were replaced
	if len(originalRequest.Hosts) != 3 {
		t.Errorf("expected 3 hosts after premodification, got %d", len(originalRequest.Hosts))
	}
	if len(originalRequest.Weights) != 3 {
		t.Errorf("expected 3 weights after premodification, got %d", len(originalRequest.Weights))
	}

	// Verify original host is no longer present
	for _, host := range originalRequest.Hosts {
		if host.ComputeHost == "original-host" {
			t.Error("original host should have been replaced")
		}
	}
	if _, exists := originalRequest.Weights["original-host"]; exists {
		t.Error("original host weight should have been replaced")
	}
}

// Test that the consumer handles missing flavor data correctly
func TestConsumerMissingFlavorData(t *testing.T) {
	consumer := &novaPipelineConsumer{Client: nil}

	request := api.PipelineRequest{
		Context: delegationAPI.NovaRequestContext{
			RequestID: "test-request-id",
		},
		Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
			Data: delegationAPI.NovaSpec{
				InstanceUUID: "test-uuid",
				Flavor: delegationAPI.NovaObject[delegationAPI.NovaFlavor]{
					Data: delegationAPI.NovaFlavor{
						Name: "", // Empty flavor name triggers missing data handling
					},
				},
			},
		},
	}

	// Should handle missing flavor data without panic and use fallback values
	consumer.Consume(request, []string{}, map[string]float64{}, map[string]map[string]float64{})
}
