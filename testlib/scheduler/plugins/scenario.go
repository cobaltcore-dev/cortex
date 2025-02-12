// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import "github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"

type MockScenario struct {
	ProjectID string
	Rebuild   bool
	Resize    bool
	Live      bool
	VMware    bool
	Hosts     []MockScenarioHost
}

func (s *MockScenario) GetProjectID() string { return s.ProjectID }
func (s *MockScenario) GetRebuild() bool     { return s.Rebuild }
func (s *MockScenario) GetResize() bool      { return s.Resize }
func (s *MockScenario) GetLive() bool        { return s.Live }
func (s *MockScenario) GetVMware() bool      { return s.VMware }
func (s *MockScenario) GetHosts() []plugins.ScenarioHost {
	hosts := make([]plugins.ScenarioHost, len(s.Hosts))
	for i, host := range s.Hosts {
		hosts[i] = plugins.ScenarioHost(&host)
	}
	return hosts
}

type MockScenarioHost struct {
	ComputeHost        string
	HypervisorHostname string
}

func (h *MockScenarioHost) GetComputeHost() string        { return h.ComputeHost }
func (h *MockScenarioHost) GetHypervisorHostname() string { return h.HypervisorHostname }
