// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

type FilterLiveMigratableStep struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, lib.EmptyFilterWeigherPipelineStepOpts]
}

// Check if a vm can be live migrated from a source to a given target host.
func (s *FilterLiveMigratableStep) checkHasSufficientFeatures(
	sourceHV hv1.Hypervisor,
	targetHV hv1.Hypervisor,
) error {

	// Needs to be the same cpu architecture
	if sourceHV.Status.Capabilities.HostCpuArch != targetHV.Status.Capabilities.HostCpuArch {
		return errors.New("cpu architectures do not match")
	}

	for _, mode := range sourceHV.Status.DomainCapabilities.SupportedCpuModes {
		if !slices.Contains(targetHV.Status.DomainCapabilities.SupportedCpuModes, mode) {
			return errors.New("cpu modes do not match")
		}
	}
	for _, feature := range sourceHV.Status.DomainCapabilities.SupportedFeatures {
		if !slices.Contains(targetHV.Status.DomainCapabilities.SupportedFeatures, feature) {
			return errors.New("hv features do not match")
		}
	}
	for _, device := range sourceHV.Status.DomainCapabilities.SupportedDevices {
		if !slices.Contains(targetHV.Status.DomainCapabilities.SupportedDevices, device) {
			return errors.New("emulated devices do not match")
		}
	}
	return nil
}

// Ensure the target host of a live migration can accept the migrating VM.
func (s *FilterLiveMigratableStep) Run(
	traceLog *slog.Logger,
	request api.ExternalSchedulerRequest,
) (*lib.FilterWeigherPipelineStepResult, error) {

	result := s.IncludeAllHostsFromRequest(request)

	intent, err := request.GetIntent()
	if err != nil {
		traceLog.Info("failed to determine request intent, skipping filter", "error", err)
		return result, nil
	}
	if intent != api.LiveMigrationIntent {
		traceLog.Info("not a live migration request, skipping filter")
		return result, nil
	}

	sourceHost, err := request.Spec.Data.GetSchedulerHintStr("source_host")
	if err != nil || sourceHost == "" {
		traceLog.Info("no source_host scheduler hint, skipping filter")
		//nolint:nilerr // Not an error we want to fail the scheduling for.
		return result, nil
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

	sourceHV, ok := hvsByName[sourceHost]
	if !ok {
		err := errors.New("source host hypervisor not found")
		traceLog.Error("failed to find source host hypervisor", "host", sourceHost, "error", err)
		return nil, err
	}
	for host := range result.Activations {
		targetHV, ok := hvsByName[host]
		if !ok {
			traceLog.Error("hypervisor not found for host", "host", host)
			delete(result.Activations, host)
			continue
		}
		if err := s.checkHasSufficientFeatures(sourceHV, targetHV); err != nil {
			delete(result.Activations, host)
			traceLog.Info(
				"filtered out host not suitable for live migration",
				"host", host,
				"reason", err.Error(),
			)
			continue
		}
		traceLog.Info("host is suitable for live migration, keeping", "host", host)
	}
	return result, nil
}

func init() {
	Index["filter_live_migratable"] = func() NovaFilter { return &FilterLiveMigratableStep{} }
}
