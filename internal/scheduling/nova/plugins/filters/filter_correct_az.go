// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
)

type FilterCorrectAZStep struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, lib.EmptyFilterWeigherPipelineStepOpts]
}

// Only get hosts in the requested az.
func (s *FilterCorrectAZStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)
	if request.Spec.Data.AvailabilityZone == "" {
		traceLog.Info("no availability zone requested, skipping filter_correct_az step")
		return result, nil
	}

	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(context.Background(), hvs); err != nil {
		traceLog.Error("failed to list hypervisors", "error", err)
		return nil, err
	}
	// The availability zone is provided by the label
	// "topology.kubernetes.io/zone" on the hv crd.
	var computeHostsInAZ = make(map[string]struct{})
	for _, hv := range hvs.Items {
		az, ok := hv.Labels[corev1.LabelTopologyZone]
		if !ok {
			traceLog.Warn("host missing zone label, keeping", "host", hv.Name)
			continue
		}
		if az == request.Spec.Data.AvailabilityZone {
			// We always assume the name of the resource corresponds
			// to the compute host name.
			computeHostsInAZ[hv.Name] = struct{}{}
		}
	}

	traceLog.Info(
		"hosts inside requested az",
		"availabilityZone", request.Spec.Data.AvailabilityZone,
		"hosts", computeHostsInAZ,
	)
	for host := range result.Activations {
		if _, ok := computeHostsInAZ[host]; ok {
			traceLog.Info("host is in requested az, keeping", "host", host)
			continue
		}
		delete(result.Activations, host)
		traceLog.Info("filtering host outside requested az", "host", host)
	}
	return result, nil
}

func init() {
	Index["filter_correct_az"] = func() NovaFilter { return &FilterCorrectAZStep{} }
}
