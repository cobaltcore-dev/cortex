// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"
	"strings"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type FilterCorrectAZStep struct {
	lib.BaseStep[api.ExternalSchedulerRequest, lib.EmptyStepOpts]
}

// Only get hosts in the requested az.
func (s *FilterCorrectAZStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.StepResult, error) {
	result := s.PrepareResult(request)
	if request.Spec.Data.AvailabilityZone == "" {
		traceLog.Debug("no availability zone requested, skipping filter_correct_az step")
		return result, nil
	}
	knowledge := &v1alpha1.Knowledge{}
	if err := s.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "host-az"},
		knowledge,
	); err != nil {
		return nil, err
	}
	hostAZs, err := v1alpha1.
		UnboxFeatureList[shared.HostAZ](knowledge.Status.Raw)
	if err != nil {
		return nil, err
	}
	var computeHostsInAZ []string
	for _, hostAZ := range hostAZs {
		if hostAZ.AvailabilityZone == nil {
			traceLog.Warn("host az knowledge has nil availability zone", "host", hostAZ.ComputeHost)
			continue
		}
		if *hostAZ.AvailabilityZone == request.Spec.Data.AvailabilityZone {
			computeHostsInAZ = append(computeHostsInAZ, hostAZ.ComputeHost)
		}
	}
	lookupStr := strings.Join(computeHostsInAZ, ",")
	for host := range result.Activations {
		if strings.Contains(lookupStr, host) {
			continue
		}
		delete(result.Activations, host)
		traceLog.Debug("filtering host outside requested az", "host", host)
	}
	return result, nil
}
