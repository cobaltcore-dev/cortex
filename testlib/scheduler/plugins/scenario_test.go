// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"testing"
)

func TestMockScenario_GetProjectID(t *testing.T) {
	scenario := MockScenario{ProjectID: "test-project"}
	if scenario.GetProjectID() != "test-project" {
		t.Errorf("expected ProjectID to be 'test-project', got %s", scenario.GetProjectID())
	}
}

func TestMockScenario_GetRebuild(t *testing.T) {
	scenario := MockScenario{Rebuild: true}
	if !scenario.GetRebuild() {
		t.Errorf("expected Rebuild to be true, got %v", scenario.GetRebuild())
	}
}

func TestMockScenario_GetResize(t *testing.T) {
	scenario := MockScenario{Resize: true}
	if !scenario.GetResize() {
		t.Errorf("expected Resize to be true, got %v", scenario.GetResize())
	}
}

func TestMockScenario_GetLive(t *testing.T) {
	scenario := MockScenario{Live: true}
	if !scenario.GetLive() {
		t.Errorf("expected Live to be true, got %v", scenario.GetLive())
	}
}

func TestMockScenario_GetVMware(t *testing.T) {
	scenario := MockScenario{VMware: true}
	if !scenario.GetVMware() {
		t.Errorf("expected VMware to be true, got %v", scenario.GetVMware())
	}
}

func TestMockScenario_GetHosts(t *testing.T) {
	hosts := []MockScenarioHost{
		{ComputeHost: "host1", HypervisorHostname: "hypervisor1"},
		{ComputeHost: "host2", HypervisorHostname: "hypervisor2"},
	}
	scenario := MockScenario{Hosts: hosts}
	result := scenario.GetHosts()

	if len(result) != len(hosts) {
		t.Fatalf("expected %d hosts, got %d", len(hosts), len(result))
	}

	for i, host := range result {
		if host.GetComputeHost() != hosts[i].ComputeHost {
			t.Errorf("expected ComputeHost to be %s, got %s", hosts[i].ComputeHost, host.GetComputeHost())
		}
		if host.GetHypervisorHostname() != hosts[i].HypervisorHostname {
			t.Errorf("expected HypervisorHostname to be %s, got %s", hosts[i].HypervisorHostname, host.GetHypervisorHostname())
		}
	}
}

func TestMockScenarioHost_GetComputeHost(t *testing.T) {
	host := MockScenarioHost{ComputeHost: "host1"}
	if host.GetComputeHost() != "host1" {
		t.Errorf("expected ComputeHost to be 'host1', got %s", host.GetComputeHost())
	}
}

func TestMockScenarioHost_GetHypervisorHostname(t *testing.T) {
	host := MockScenarioHost{HypervisorHostname: "hypervisor1"}
	if host.GetHypervisorHostname() != "hypervisor1" {
		t.Errorf("expected HypervisorHostname to be 'hypervisor1', got %s", host.GetHypervisorHostname())
	}
}
