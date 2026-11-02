// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"github.com/go-logr/logr"
	"slices"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

type FilterHasRequestedTraits struct {
	lib.BaseFilter[compute.SchedulerRequest, lib.EmptyFilterWeigherPipelineStepOpts]
}

// Filter hosts based on the traits requested in the flavor:
// - a forbidden trait means the host must not have the specified trait.
// - a required trait means the host must have the specified trait.
func (s *FilterHasRequestedTraits) Run(traceLog logr.Logger, request compute.SchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)
	requiredTraits := request.RequiredTraits
	forbiddenTraits := request.ForbiddenTraits
	if len(requiredTraits) == 0 && len(forbiddenTraits) == 0 {
		traceLog.V(1).Info("no traits requested, skipping filter")
		return result, nil
	}
	traceLog.V(1).Info(
		"filtering hosts based on requested traits",
		"required", requiredTraits,
		"forbidden", forbiddenTraits,
	)

	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(context.Background(), hvs); err != nil {
		traceLog.Error(err, "failed to list hypervisors")
		return nil, err
	}

	hostsMatchingAllTraits := map[string]struct{}{}
	for _, hv := range hvs.Items {
		if _, ok := result.Activations[hv.Name]; !ok {
			continue
		}
		allRequiredPresent := true
		traits := hv.Status.Traits
		traits = append(traits, hv.Spec.CustomTraits...)
		for _, required := range requiredTraits {
			if !slices.Contains(traits, required) {
				allRequiredPresent = false
				break
			}
		}
		allForbiddenAbsent := true
		for _, forbidden := range forbiddenTraits {
			if slices.Contains(traits, forbidden) {
				allForbiddenAbsent = false
				break
			}
		}
		if allRequiredPresent && allForbiddenAbsent {
			hostsMatchingAllTraits[hv.Name] = struct{}{}
		}
	}

	for host := range result.Activations {
		if _, ok := hostsMatchingAllTraits[host]; ok {
			traceLog.V(1).Info("host matches requested traits, keeping", "host", host)
			continue
		}
		delete(result.Activations, host)
		traceLog.V(1).Info("filtering host not matching requested traits", "host", host)
	}
	return result, nil
}

func init() {
	Index["filter_has_requested_traits"] = func() NovaFilter { return &FilterHasRequestedTraits{} }
}
