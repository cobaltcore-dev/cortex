// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

type FilterComputeCapabilitiesStep struct {
	lib.BaseStep[api.ExternalSchedulerRequest, lib.EmptyStepOpts]
}

// Check the capabilities of each host and if they match the extra spec provided
// in the request spec flavor.
func (s *FilterComputeCapabilitiesStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.StepResult, error) {
	result := s.PrepareResult(request)
	requestedCapabilities := request.Spec.Data.Flavor.Data.ExtraSpecs
	if len(requestedCapabilities) == 0 {
		traceLog.Debug("no flavor extra spec capabilities in request, skipping filter")
		return result, nil
	}

	// Note: currently none of the advanced operators for capabilities are
	// supported because they are not used by any of our flavors in production.
	// Ops: https://github.com/sapcc/nova/blob/3ebf80/nova/scheduler/filters/extra_specs_ops.py#L23
	unsupportedOps := []string{
		"=", "<in>", "<all-in>", "==", "!=", ">=", "<=",
		"s==", "s!=", "s<", "s<=", "s>", "s>=", "<or>", // or is special
	}
	for key, expr := range requestedCapabilities {
		if !strings.HasPrefix(key, "capabilities:") {
			delete(requestedCapabilities, key) // Remove non-capability keys.
		}
		for _, op := range unsupportedOps {
			if strings.Contains(expr, op) {
				traceLog.Warn(
					"unsupported extra spec operator in capabilities filter, skipping filter",
					"key", key, "expr", expr, "flavor", request.Spec.Data.Flavor,
				)
				return result, nil
			}
		}
	}

	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(context.Background(), hvs); err != nil {
		traceLog.Error("failed to list hypervisors", "error", err)
		return nil, err
	}

	// We take the `capabilities` field from the hypervisor status and
	// flatten it to a map[string]any where keys are prefixed with `capabilities:`.
	// This allows us to directly compare with the requested extra specs.
	providedCapabilities := make(map[string]map[string]any)
	for _, hv := range hvs.Items {
		marshalled, err := json.Marshal(hv.Status.Capabilities)
		if err != nil {
			traceLog.Error("failed to marshal hypervisor capabilities", "host", hv.Name, "error", err)
			continue
		}
		cpuInfo := make(map[string]any)
		if err := json.Unmarshal(marshalled, &cpuInfo); err != nil {
			traceLog.Error("failed to unmarshal hypervisor capabilities", "host", hv.Name, "error", err)
			continue
		}
		providedCapabilities[hv.Name] = make(map[string]any)
		for key, value := range cpuInfo {
			providedCapabilities[hv.Name]["capabilities:"+key] = value
		}
	}
	traceLog.Info(
		"provided capabilities from hypervisors",
		"capabilities", providedCapabilities,
	)

	// Check which hosts match the requested capabilities.
	for host := range result.Activations {
		provided, ok := providedCapabilities[host]
		if !ok {
			delete(result.Activations, host)
			traceLog.Info("filtering host without provided capabilities", "host", host)
			continue
		}
		// Check if the provided capabilities match the requested ones.
		for keyRequested, valueRequested := range requestedCapabilities {
			if providedValue, ok := provided[keyRequested]; !ok || providedValue != valueRequested {
				traceLog.Info(
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
