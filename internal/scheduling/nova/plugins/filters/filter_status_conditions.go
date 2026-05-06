// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type FilterStatusConditionsStep struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, lib.EmptyFilterWeigherPipelineStepOpts]
}

// Check that all status conditions meet the expected values, for example,
// that the hypervisor is ready and not disabled.
func (s *FilterStatusConditionsStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)

	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(context.Background(), hvs); err != nil {
		traceLog.Error("failed to list hypervisors", "error", err)
		return nil, err
	}

	expected := map[string]metav1.ConditionStatus{
		hv1.ConditionTypeReady:              metav1.ConditionTrue,
		hv1.ConditionTypeHypervisorDisabled: metav1.ConditionFalse,
		hv1.ConditionTypeTerminating:        metav1.ConditionFalse,
		// The hypervisor tainted condition is set when users touch the resource
		// via kubectl, and shouldn't impact if we can schedule on this hypervisor.
		hv1.ConditionTypeTainted:           "",
		hv1.ConditionTypeOnboarding:        "", // Don't care
		hv1.ConditionTypeTraitsUpdated:     "", // Don't care
		hv1.ConditionTypeAggregatesUpdated: "", // Don't care
	}

	var hostsReady = make(map[string]struct{})
	for _, hv := range hvs.Items {
		allMet := true
		for conditionType, expectedStatus := range expected {
			cd := meta.FindStatusCondition(hv.Status.Conditions, conditionType)
			if cd == nil {
				traceLog.Info(
					"hypervisor missing condition, keeping",
					"host", hv.Name, "condition", conditionType,
				)
				// TODO: Decide if we want to filter hosts missing conditions
				// or not. For now we keep them.
				continue
			}
			if expectedStatus == "" {
				continue // Don't care about this condition
			}
			if cd.Status != expectedStatus {
				traceLog.Info(
					"hypervisor condition not met, filtering host",
					"host", hv.Name,
					"condition", conditionType,
					"status", cd.Status,
				)
				allMet = false
				break
			}
		}
		if allMet {
			traceLog.Info("hypervisor meets all status conditions, keeping host", "host", hv.Name)
			hostsReady[hv.Name] = struct{}{}
		}
	}

	traceLog.Info("hosts passing status conditions filter", "hosts", hostsReady)
	for host := range result.Activations {
		if _, ok := hostsReady[host]; ok {
			continue
		}
		delete(result.Activations, host)
	}
	return result, nil
}

func init() {
	Index["filter_status_conditions"] = func() NovaFilter { return &FilterStatusConditionsStep{} }
}
