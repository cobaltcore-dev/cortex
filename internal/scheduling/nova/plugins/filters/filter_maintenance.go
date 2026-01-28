// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

type FilterMaintenanceStep struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, lib.EmptyFilterWeigherPipelineStepOpts]
}

// Check that the maintenance spec of the hypervisor doesn't prevent scheduling.
func (s *FilterMaintenanceStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)

	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(context.Background(), hvs); err != nil {
		traceLog.Error("failed to list hypervisors", "error", err)
		return nil, err
	}

	flagsPreventingScheduling := map[string]bool{
		hv1.MaintenanceUnset:       false,
		hv1.MaintenanceManual:      true,
		hv1.MaintenanceAuto:        false,
		hv1.MaintenanceHA:          false,
		hv1.MaintenanceTermination: true,
	}

	var hostsReady = make(map[string]struct{})
	for _, hv := range hvs.Items {
		preventScheduling, ok := flagsPreventingScheduling[hv.Spec.Maintenance]
		if !ok {
			traceLog.Info(
				"hypervisor has unknown maintenance flag, filtering host",
				"host", hv.Name, "maintenance", hv.Spec.Maintenance,
			)
			continue
		}
		if preventScheduling {
			traceLog.Info(
				"hypervisor maintenance flag prevents scheduling, filtering host",
				"host", hv.Name, "maintenance", hv.Spec.Maintenance,
			)
			continue
		}
		hostsReady[hv.Name] = struct{}{}
	}

	traceLog.Info("hosts passing maintenance filter", "hosts", hostsReady)
	for host := range result.Activations {
		if _, ok := hostsReady[host]; ok {
			continue
		}
		delete(result.Activations, host)
	}
	return result, nil
}

func init() {
	Index["filter_maintenance"] = func() NovaFilter { return &FilterMaintenanceStep{} }
}
