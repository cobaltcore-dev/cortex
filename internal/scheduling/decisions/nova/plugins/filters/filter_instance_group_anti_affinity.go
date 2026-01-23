// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"
	"slices"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

type FilterInstanceGroupAntiAffinityStep struct {
	lib.BaseStep[api.ExternalSchedulerRequest, lib.EmptyStepOpts]
}

// Select hosts not in spec_obj.instance_group but only until
// max_server_per_host is reached (by default = 1).
func (s *FilterInstanceGroupAntiAffinityStep) Run(
	traceLog *slog.Logger,
	request api.ExternalSchedulerRequest,
) (*lib.StepResult, error) {

	result := s.IncludeAllHostsFromRequest(request)

	ig := request.Spec.Data.InstanceGroup
	if ig == nil {
		traceLog.Debug("no instance group in request, skipping filter")
		return result, nil
	}
	policy := ig.Data.Policy
	if policy != "anti-affinity" {
		traceLog.Debug("instance group policy is not 'anti-affinity', skipping filter")
		return result, nil
	}
	memberVMs := ig.Data.Members
	if len(memberVMs) == 0 {
		// Nothing to do.
		traceLog.Debug("instance group has no members, skipping filter")
		return result, nil
	}
	maxServersPerHost := 1
	if ig.Data.Rules != nil {
		if maxServersPerHostAny, ok := ig.Data.Rules["max_server_per_host"]; ok {
			if maxServersPerHostInt, ok := maxServersPerHostAny.(int); ok {
				maxServersPerHost = maxServersPerHostInt
			}
		}
	}

	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(context.Background(), hvs); err != nil {
		traceLog.Error("failed to list hypervisors", "error", err)
		return nil, err
	}
	hvsByName := make(map[string]hv1.Hypervisor)
	for _, hv := range hvs.Items {
		hvsByName[hv.Name] = hv
	}

	for host := range result.Activations {
		hv, ok := hvsByName[host]
		if !ok {
			traceLog.Error("hypervisor not found for host", "host", host)
			delete(result.Activations, host)
			continue
		}
		// Check if this host is already running the same vm (resizes).
		// In this case we should not filter it out.
		if slices.ContainsFunc(hv.Status.Instances, func(inst hv1.Instance) bool {
			return inst.ID == request.Spec.Data.InstanceUUID
		}) {
			traceLog.Debug("host is running the same VM, not filtering out", "host", host)
			continue
		}
		// Check how many instances from the group are already on this host.
		instancesInGroup := []string{}
		for _, inst := range hv.Status.Instances {
			if slices.Contains(memberVMs, inst.ID) {
				instancesInGroup = append(instancesInGroup, inst.ID)
			}
		}
		if len(instancesInGroup) >= maxServersPerHost {
			delete(result.Activations, host)
			traceLog.Info(
				"filtered out host exceeding max_server_per_host for instance group",
				"host", host,
				"instances_in_group", instancesInGroup,
				"max_server_per_host", maxServersPerHost,
			)
		}
	}
	return result, nil
}
