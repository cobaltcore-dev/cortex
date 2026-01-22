// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

type FilterHasRequestedTraits struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, lib.EmptyStepOpts]
}

// Filter hosts that do not have the requested traits given by the extra spec:
// - "trait:<trait>": "forbidden" means the host must not have the specified trait.
// - "trait:<trait>": "required" means the host must have the specified trait.
func (s *FilterHasRequestedTraits) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.StepResult, error) {
	result := s.PrepareResult(request)
	var requiredTraits, forbiddenTraits []string
	for key, value := range request.Spec.Data.Flavor.Data.ExtraSpecs {
		if !strings.HasPrefix(key, "trait:") {
			continue
		}
		traitName := strings.TrimPrefix(key, "trait:")
		switch value {
		case "forbidden":
			forbiddenTraits = append(forbiddenTraits, traitName)
		case "required":
			requiredTraits = append(requiredTraits, traitName)
		}
	}
	if len(requiredTraits) == 0 && len(forbiddenTraits) == 0 {
		traceLog.Debug("no traits requested, skipping filter")
		return result, nil
	}

	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(context.Background(), hvs); err != nil {
		traceLog.Error("failed to list hypervisors", "error", err)
		return nil, err
	}

	hostsMatchingAllTraits := map[string]struct{}{}
	for _, hv := range hvs.Items {
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

	traceLog.Info("hosts matching requested traits", "hosts", hostsMatchingAllTraits)
	for host := range result.Activations {
		if _, ok := hostsMatchingAllTraits[host]; ok {
			continue
		}
		delete(result.Activations, host)
		traceLog.Info("filtering host not matching requested traits", "host", host)
	}
	return result, nil
}
