// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"
	"strings"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type FilterHasRequestedTraits struct {
	lib.BaseStep[api.ExternalSchedulerRequest, lib.EmptyStepOpts]
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
	knowledge := &v1alpha1.Knowledge{}
	if err := s.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "host-capabilities"},
		knowledge,
	); err != nil {
		return nil, err
	}
	hostCapabilities, err := v1alpha1.
		UnboxFeatureList[compute.HostCapabilities](knowledge.Status.Raw)
	if err != nil {
		return nil, err
	}
	hostsEncountered := map[string]struct{}{}
	for _, cap := range hostCapabilities {
		hostsEncountered[cap.ComputeHost] = struct{}{}
		providedTraits := cap.Traits // Comma-separated list.
		allRequiredPresent := true
		for _, required := range requiredTraits {
			if !strings.Contains(providedTraits, required) {
				allRequiredPresent = false
				break
			}
		}
		allForbiddenAbsent := true
		for _, forbidden := range forbiddenTraits {
			if strings.Contains(providedTraits, forbidden) {
				allForbiddenAbsent = false
				break
			}
		}
		if !allRequiredPresent || !allForbiddenAbsent {
			delete(result.Activations, cap.ComputeHost)
			traceLog.Debug(
				"filtering host which does not match trait check",
				"host", cap.ComputeHost, "want", requiredTraits,
				"forbid", forbiddenTraits, "have", providedTraits,
			)
		}
	}
	// Remove all hosts that weren't encountered.
	for host := range result.Activations {
		if _, ok := hostsEncountered[host]; !ok {
			delete(result.Activations, host)
			traceLog.Debug(
				"removing host with unknown capabilities",
				"host", host,
			)
		}
	}
	return result, nil
}
