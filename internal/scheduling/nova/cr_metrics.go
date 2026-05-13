// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/resource"

	ctrl "sigs.k8s.io/controller-runtime"
)

// NewNoHostFoundCounter creates the Prometheus counter for no-host-found classification.
// Register it with the metrics registry before assigning it to the controller.
func NewNoHostFoundCounter() *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cortex_nova_no_host_found_total",
		Help: "Nova no-host-found results classified by committed resource coverage (no_cr/cr_exhausted/slot_exhausted/slot_blocked/error).",
	}, []string{"case", "flavor_group", "intent"})
}

// NewPlacementCounter creates the Prometheus counter for successful Nova placements.
// Labels: flavor_group, intent, cr_slot (no_cr/slot_missed/slot_used/error). PAYG placements
// (flavor not in any group) are not counted — they return before reaching this counter.
// cr_slot=error is emitted when the flavor group lookup fails due to a K8s error.
// Register it with the metrics registry before assigning it to the controller.
func NewPlacementCounter() *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cortex_nova_placement_total",
		Help: "Successful Nova placements by committed resource slot outcome.",
	}, []string{"flavor_group", "intent", "cr_slot"})
}

// classifyNoHostFound determines why no host was found for a nova placement request,
// in terms of committed resource coverage:
//
//   - no_cr: project has no active CommittedResources for the flavor group (NH1)
//   - cr_exhausted: CommittedResources exist but are fully occupied (NH2)
//   - slot_exhausted: CR has remaining capacity but no input host has a usable slot (NH3)
//   - slot_blocked: a usable slot exists but scheduling constraints excluded all such hosts (NH4)
func classifyNoHostFound(
	activeCRs []v1alpha1.CommittedResource,
	evaluator *CRSlotEvaluator,
	inputHosts []string,
	projectID, flavorGroupName string,
	vmMemBytes int64,
) string {

	if len(activeCRs) == 0 {
		return "no_cr"
	}

	totalCapacity := resource.Quantity{}
	totalUsed := resource.Quantity{}
	for _, cr := range activeCRs {
		totalCapacity.Add(cr.Spec.Amount)
		if used, ok := cr.Status.UsedResources["memory"]; ok {
			totalUsed.Add(used)
		}
	}
	if totalUsed.Cmp(totalCapacity) >= 0 {
		return "cr_exhausted"
	}

	for _, host := range inputHosts {
		if evaluator.HasUsableSlot(host, projectID, flavorGroupName, vmMemBytes) {
			return "slot_blocked"
		}
	}
	return "slot_exhausted"
}

// logNoHostFound classifies a no-host-found result and emits a log line and metric.
func (c *FilterWeigherPipelineController) logNoHostFound(ctx context.Context, decision *v1alpha1.Decision, request api.ExternalSchedulerRequest) {
	log := ctrl.LoggerFrom(ctx)

	projectID := request.Context.ProjectID
	flavorName := request.Spec.Data.Flavor.Data.Name
	instanceUUID := request.Spec.Data.InstanceUUID
	intent := decision.Spec.Intent

	flavorGroupName, flavorInGroup, err := c.resolveFlavorGroup(ctx, flavorName)
	if err != nil {
		if errors.Is(err, errFlavorNotInGroup) {
			return // PAYG: flavor has no group, no metric
		}
		log.Error(err, "no-host-found: failed to resolve flavor group",
			"instanceUUID", instanceUUID, "flavor", flavorName)
		if c.NoHostFoundCounter != nil {
			c.NoHostFoundCounter.WithLabelValues("error", "unknown", string(intent)).Inc()
		}
		return
	}

	vmMemBytes := int64(flavorInGroup.MemoryMB) * 1024 * 1024 //nolint:gosec // flavor memory bounded by specs

	var crList v1alpha1.CommittedResourceList
	if err := c.List(ctx, &crList); err != nil {
		log.Error(err, "no-host-found: failed to list committed resources", "instanceUUID", instanceUUID)
		return
	}
	var activeCRs []v1alpha1.CommittedResource
	for _, cr := range crList.Items {
		if !cr.MatchesGroup(projectID, flavorGroupName) || !cr.IsActive() {
			continue
		}
		activeCRs = append(activeCRs, cr)
	}

	evaluator, err := BuildCRSlotEvaluator(ctx, c.Client)
	if err != nil {
		log.Error(err, "no-host-found: failed to build CR slot evaluator", "instanceUUID", instanceUUID)
		return
	}

	noHostFoundCase := classifyNoHostFound(activeCRs, evaluator, request.GetHosts(), projectID, flavorGroupName, vmMemBytes)

	log.Info("no-host-found classified",
		"case", noHostFoundCase,
		"instanceUUID", instanceUUID,
		"projectID", projectID,
		"flavorGroup", flavorGroupName,
		"intent", intent,
	)
	if c.NoHostFoundCounter != nil {
		c.NoHostFoundCounter.WithLabelValues(noHostFoundCase, flavorGroupName, string(intent)).Inc()
	}
}
