// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"github.com/go-logr/logr"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
)

type FilterCorrectAZStep struct {
	lib.BaseFilter[compute.PlacementRequest, lib.EmptyFilterWeigherPipelineStepOpts]
}

// Only get hosts in the requested az.
func (s *FilterCorrectAZStep) Run(traceLog logr.Logger, request compute.PlacementRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)
	if request.AvailabilityZone == "" {
		traceLog.V(1).Info("no availability zone requested, skipping filter_correct_az step")
		return result, nil
	}

	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(context.Background(), hvs); err != nil {
		traceLog.Error(err, "failed to list hypervisors")
		return nil, err
	}
	// The availability zone is provided by the label
	// "topology.kubernetes.io/zone" on the hv crd.
	var computeHostsInAZ = make(map[string]struct{})
	for _, hv := range hvs.Items {
		if _, ok := result.Activations[hv.Name]; !ok {
			continue
		}
		az, ok := hv.Labels[corev1.LabelTopologyZone]
		if !ok {
			traceLog.V(1).Info("host missing zone label, keeping", "host", hv.Name)
			continue
		}
		if az == request.AvailabilityZone {
			// We always assume the name of the resource corresponds
			// to the compute host name.
			computeHostsInAZ[hv.Name] = struct{}{}
		}
	}

	traceLog.V(1).Info(
		"hosts inside requested az",
		"availabilityZone", request.AvailabilityZone,
		"hosts", computeHostsInAZ,
	)
	for host := range result.Activations {
		if _, ok := computeHostsInAZ[host]; ok {
			traceLog.V(1).Info("host is in requested az, keeping", "host", host)
			continue
		}
		delete(result.Activations, host)
		traceLog.V(1).Info("filtering host outside requested az", "host", host)
	}
	return result, nil
}

func init() {
	Index["filter_correct_az"] = func() NovaFilter { return &FilterCorrectAZStep{} }
}
