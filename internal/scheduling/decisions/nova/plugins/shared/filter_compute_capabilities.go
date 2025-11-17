// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"encoding/json"
	"log/slog"
	"maps"
	"strings"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type FilterComputeCapabilitiesStep struct {
	lib.BaseStep[api.ExternalSchedulerRequest, lib.EmptyStepOpts]
}

// Convert a nested dictionary into a list of capabilities.
//
// The input is something like this:
//
//	{
//	    "arch": "x86_64",
//	    "maxphysaddr": {"bits": 46},
//	    ...
//	}
//
// Which then outputs a list of capabilities like:
// {"arch": "x86_64", "maxphysaddr:bits": 46, ...}
func convertToCapabilities(prefix string, obj map[string]any) map[string]any {
	capabilities := make(map[string]any)
	for key, value := range obj {
		if subObj, ok := value.(map[string]any); ok {
			// Nested object.
			subCapabilities := convertToCapabilities(prefix+key+":", subObj)
			maps.Copy(capabilities, subCapabilities)
		} else {
			// Flat value.
			capabilities[prefix+key] = value
		}
	}
	return capabilities
}

// Check the capabilities of each host and if they match the extra spec provided
// in the request spec flavor.
func (s *FilterComputeCapabilitiesStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.StepResult, error) {
	result := s.PrepareResult(request)
	requestedCapabilities := request.Spec.Data.Flavor.Data.ExtraSpecs
	// Note: currently advanced operators for the capabilities are not supported
	// because they are not used by any of our flavors in production.
	for key := range requestedCapabilities {
		if !strings.HasPrefix(key, "capabilities:") {
			delete(requestedCapabilities, key) // Remove non-capability keys.
		}
	}
	if len(requestedCapabilities) == 0 {
		traceLog.Debug("no flavor extra spec capabilities in request, skipping filter")
		return result, nil
	}
	var hypervisors []nova.Hypervisor
	if _, err := s.DB.SelectTimed(
		"scheduler-nova", &hypervisors, "SELECT * FROM "+nova.Hypervisor{}.TableName(),
	); err != nil {
		return result, err
	}
	// Serialize the hypervisor fields that are interesting for the filter.
	providedCapabilities := make(map[string]map[string]any)
	for _, h := range hypervisors {
		// It is assumed that multiple hypervisors have the same capabilities
		// when they are nested in the same compute host.
		if _, ok := providedCapabilities[h.ServiceHost]; ok {
			continue // Already processed this compute host.
		}
		// Uwrap the cpu capabilities.
		var cpuInfo map[string]any
		if h.CPUInfo != "" {
			if err := json.Unmarshal([]byte(h.CPUInfo), &cpuInfo); err != nil {
				traceLog.Warn("failed to unmarshal CPU info", "hv", h.ID, "error", err)
				return result, err
			}
		} else {
			cpuInfo = make(map[string]any)
		}
		// Note that Nova flavors directly map the cpu_info fields to extra
		// specs, without a nested `capabilities:cpu_info` prefix.
		cs := convertToCapabilities("capabilities:", cpuInfo)
		cs["capabilities:hypervisor_type"] = h.HypervisorType
		cs["capabilities:hypervisor_version"] = h.HypervisorVersion
		providedCapabilities[h.ServiceHost] = cs
	}
	// Check which hosts match the requested capabilities.
	for host := range result.Activations {
		provided, ok := providedCapabilities[host]
		if !ok {
			delete(result.Activations, host)
			traceLog.Debug("filtering host without provided capabilities", "host", host)
			continue
		}
		// Check if the provided capabilities match the requested ones.
		for keyRequested, valueRequested := range requestedCapabilities {
			if providedValue, ok := provided[keyRequested]; !ok || providedValue != valueRequested {
				traceLog.Debug(
					"filtering host with mismatched capabilities", "host", host,
					"wantKey", keyRequested, "wantValue", valueRequested,
					"haveKey?", ok, "haveValue", providedValue,
				)
				delete(result.Activations, host)
				break
			}
		}
	}
	return result, nil
}
