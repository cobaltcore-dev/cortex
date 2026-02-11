// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

type FilterCapabilitiesStep struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, lib.EmptyFilterWeigherPipelineStepOpts]
}

// Get the provided capabilities of a hypervisor resource in the format Nova expects.
// The resulting map has keys like "capabilities:key" to match flavor extra specs.
// For example, if the hypervisor provides a cpu architecture "x86_64",
// the resulting map will have an entry "capabilities:cpu_info": "x86_64".
func hvToNovaCapabilities(hv hv1.Hypervisor) (map[string]string, error) {
	caps := make(map[string]string)

	// Nova example: capabilities:hypervisor_type='CH'
	// Value provided by libvirt domain capabilities: 'ch'
	switch hv.Status.DomainCapabilities.HypervisorType {
	case "ch":
		caps["capabilities:hypervisor_type"] = "CH"
	case "qemu":
		caps["capabilities:hypervisor_type"] = "QEMU"
	default:
		return nil, fmt.Errorf("unknown autodiscovered hypervisor type: %s", hv.Status.DomainCapabilities.HypervisorType)
	}

	// Nova example: capabilities:cpu_arch='x86_64'
	caps["capabilities:cpu_arch"] = hv.Status.Capabilities.HostCpuArch

	return caps, nil
}

// Check the capabilities of each host and if they match the extra spec provided
// in the request spec flavor.
func (s *FilterCapabilitiesStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)
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

	hvCaps := make(map[string]map[string]string)
	for _, hv := range hvs.Items {
		var err error
		if hvCaps[hv.Name], err = hvToNovaCapabilities(hv); err != nil {
			traceLog.Error("failed to get nova capabilities from hypervisor", "host", hv.Name, "error", err)
			return nil, err
		}
	}
	traceLog.Info("looking for capabilities", "capabilities", hvCaps)

	// Check which hosts match the requested capabilities.
	for host := range result.Activations {
		provided, ok := hvCaps[host]
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
		traceLog.Info("host matches requested capabilities, keeping", "host", host)
	}
	return result, nil
}

func init() {
	Index["filter_capabilities"] = func() NovaFilter { return &FilterCapabilitiesStep{} }
}
