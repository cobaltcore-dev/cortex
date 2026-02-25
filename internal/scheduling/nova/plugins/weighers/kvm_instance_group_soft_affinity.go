// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"context"
	"log/slog"
	"slices"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

// This weigher implements the "soft affinity" and "soft anti-affinity" policy
// for instance groups in nova.
//
// It assigns a weight to each host based on how many instances of the same
// instance group are already running on that host. The more instances of the
// same group on a host, the lower (for soft-anti-affinity) or higher
// (for soft-affinity) the weight, which makes it less likely or more likely,
// respectively, for the scheduler to choose that host for new instances of
// the same group.
type KVMInstanceGroupSoftAffinityStep struct {
	lib.BaseWeigher[api.ExternalSchedulerRequest, lib.EmptyFilterWeigherPipelineStepOpts]
}

func (s *KVMInstanceGroupSoftAffinityStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)
	result.Statistics["affinity"] = s.PrepareStats(request, "float")

	ig := request.Spec.Data.InstanceGroup
	if ig == nil {
		traceLog.Info("no instance group in request, skipping weigher")
		return result, nil
	}
	policy := ig.Data.Policy
	var factor float64
	switch policy {
	case "soft-anti-affinity":
		factor = -1.0
	case "soft-affinity":
		factor = 1.0
	default:
		traceLog.Info("instance group policy is not 'soft-affinity' or 'soft-anti-affinity', skipping weigher", "policy", policy)
		return result, nil
	}
	if len(ig.Data.Members) == 0 {
		traceLog.Info("instance group has no members, skipping weigher")
		return result, nil
	}

	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(context.Background(), hvs); err != nil {
		traceLog.Error("failed to list hypervisors", "error", err)
		return nil, err
	}
	hvsByName := make(map[string]hv1.Hypervisor, len(hvs.Items))
	for _, hv := range hvs.Items {
		hvsByName[hv.Name] = hv
	}

	for host := range result.Activations {
		hv, ok := hvsByName[host]
		if !ok {
			traceLog.Info("host not found in hypervisor list, skipping", "host", host)
			continue
		}
		count := 0
		for _, instance := range hv.Status.Instances {
			if slices.Contains(ig.Data.Members, instance.ID) {
				count++
			}
		}
		weight := factor * float64(count)
		result.Activations[host] = weight
		result.Statistics["affinity"].Hosts[host] = weight
		traceLog.Info("calculated affinity weight for host",
			"host", host, "count", count, "weight", weight)
	}
	return result, nil
}

func init() {
	Index["kvm_instance_group_soft_affinity"] = func() NovaWeigher { return &KVMInstanceGroupSoftAffinityStep{} }
}
