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
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NewNoHostFoundCounter creates the Prometheus counter for no-host-found classification.
// Register it with the metrics registry before assigning it to the controller.
func NewNoHostFoundCounter() *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cortex_nova_no_host_found_total",
		Help: "Nova no-host-found results classified by committed resource coverage (cases A/B/C/D).",
	}, []string{"case", "flavor_group", "intent"})
}

// classifyNoHostFound determines why no host was found for a nova placement request,
// in terms of committed resource coverage:
//
//   - D: project has no active CommittedResources for the flavor group
//   - A: CommittedResources exist but are fully occupied (used >= capacity)
//   - B: CommittedResources have remaining capacity but no free Reservation slot
//   - C: free Reservation slots exist but placement constraints excluded all candidates
func classifyNoHostFound(
	activeCRs []v1alpha1.CommittedResource,
	reservations []v1alpha1.Reservation,
	projectID, flavorGroupName string,
) string {

	if len(activeCRs) == 0 {
		return "D"
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
		return "A"
	}

	for _, res := range reservations {
		cr := res.Spec.CommittedResourceReservation
		if cr == nil || cr.ProjectID != projectID || cr.ResourceGroup != flavorGroupName {
			continue
		}
		if reservationRemainingMemory(res) > 0 {
			return "C"
		}
	}
	return "B"
}

// logNoHostFound classifies a no-host-found result and emits a log line and metric.
func (c *FilterWeigherPipelineController) logNoHostFound(ctx context.Context, decision *v1alpha1.Decision, request api.ExternalSchedulerRequest) {
	log := ctrl.LoggerFrom(ctx)

	projectID := request.Context.ProjectID
	flavorName := request.Spec.Data.Flavor.Data.Name
	instanceUUID := request.Spec.Data.InstanceUUID
	intent := decision.Spec.Intent

	flavorGroupName, _, err := c.resolveFlavorGroup(ctx, flavorName)
	if err != nil {
		if errors.Is(err, errFlavorNotInGroup) {
			log.V(1).Info("no-host-found: PAYG flavor, not CR-relevant",
				"instanceUUID", instanceUUID, "flavor", flavorName, "intent", intent)
		} else {
			log.Error(err, "no-host-found: failed to resolve flavor group",
				"instanceUUID", instanceUUID, "flavor", flavorName)
		}
		return
	}

	var crList v1alpha1.CommittedResourceList
	if err := c.List(ctx, &crList); err != nil {
		log.Error(err, "no-host-found: failed to list committed resources", "instanceUUID", instanceUUID)
		return
	}
	var activeCRs []v1alpha1.CommittedResource
	for _, cr := range crList.Items {
		if cr.Spec.ProjectID != projectID || cr.Spec.FlavorGroupName != flavorGroupName {
			continue
		}
		if cr.Spec.State != v1alpha1.CommitmentStatusConfirmed && cr.Spec.State != v1alpha1.CommitmentStatusGuaranteed {
			continue
		}
		activeCRs = append(activeCRs, cr)
	}

	var reservationList v1alpha1.ReservationList
	if err := c.List(ctx, &reservationList,
		client.MatchingLabels{v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource},
	); err != nil {
		log.Error(err, "no-host-found: failed to list reservations", "instanceUUID", instanceUUID)
		return
	}

	noHostFoundCase := classifyNoHostFound(activeCRs, reservationList.Items, projectID, flavorGroupName)

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
