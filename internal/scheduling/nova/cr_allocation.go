// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"
	"fmt"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// recordCRAllocation writes the placed VM UUID into the matching Reservation
// Spec.CommittedResourceReservation.Allocations after a successful Nova placement.
func (c *FilterWeigherPipelineController) recordCRAllocation(ctx context.Context, decision *v1alpha1.Decision, request api.ExternalSchedulerRequest) {
	log := ctrl.LoggerFrom(ctx)

	instanceUUID := request.Spec.Data.InstanceUUID
	projectID := request.Context.ProjectID
	flavorName := request.Spec.Data.Flavor.Data.Name
	selectedHost := *decision.Status.Result.TargetHost
	intent := string(decision.Spec.Intent)

	flavorGroupName, flavorInGroup, err := c.resolveFlavorGroup(ctx, flavorName)
	if err != nil {
		if errors.Is(err, errFlavorNotInGroup) {
			log.V(1).Info("CR allocation: flavor not in any group, PAYG placement", "flavor", flavorName)
		} else {
			log.Error(err, "CR allocation: failed to resolve flavor group",
				"flavor", flavorName, "instanceUUID", instanceUUID)
			if c.PlacementCounter != nil {
				c.PlacementCounter.WithLabelValues("unknown", intent, "error").Inc()
			}
		}
		return
	}

	evaluator, err := BuildCRSlotEvaluator(ctx, c.Client)
	if err != nil {
		log.Error(err, "CR allocation: failed to build CR slot evaluator", "instanceUUID", instanceUUID)
		return
	}

	var crList v1alpha1.CommittedResourceList
	if err := c.List(ctx, &crList); err != nil {
		log.Error(err, "CR allocation: failed to list committed resources", "instanceUUID", instanceUUID)
		return
	}
	var activeCRs []v1alpha1.CommittedResource
	for _, cr := range crList.Items {
		if !cr.MatchesGroup(projectID, flavorGroupName) || !cr.IsActive() {
			continue
		}
		activeCRs = append(activeCRs, cr)
	}

	candidateHosts := decision.Status.Result.OrderedHosts
	crOutcome := classifyCRPlacement(evaluator, activeCRs, candidateHosts, projectID, flavorGroupName)

	log.V(1).Info("CR allocation: placement classified",
		"cr_outcome", crOutcome,
		"instanceUUID", instanceUUID,
		"host", selectedHost,
		"projectID", projectID,
		"flavorGroup", flavorGroupName,
	)
	if c.PlacementCounter != nil {
		c.PlacementCounter.WithLabelValues(flavorGroupName, intent, crOutcome).Inc()
	}

	if crOutcome != "slot_used" {
		return
	}

	slotsOnTarget := evaluator.SlotsForHost(selectedHost, projectID, flavorGroupName)

	for _, slot := range slotsOnTarget {
		if _, exists := slot.Spec.CommittedResourceReservation.Allocations[instanceUUID]; exists {
			log.Info("CR allocation: VM UUID already in reservation, skipping",
				"instanceUUID", instanceUUID, "reservation", slot.Name)
			return
		}
	}

	vmMemoryBytes := int64(flavorInGroup.MemoryMB) * 1024 * 1024 //nolint:gosec // flavor memory bounded by specs
	vmCPUs := int64(flavorInGroup.VCPUs)                         //nolint:gosec // VCPUs bounded by specs

	slotName := pickReservationSlot(slotsOnTarget, vmMemoryBytes)
	if slotName == "" {
		log.V(1).Info("CR allocation: slot_used but target host has no slot with remaining capacity",
			"instanceUUID", instanceUUID, "host", selectedHost)
		return
	}

	log.Info("CR allocation: writing VM UUID into reservation",
		"instanceUUID", instanceUUID, "reservation", slotName,
		"projectID", projectID, "flavorGroup", flavorGroupName, "host", selectedHost)

	vmResources := map[hv1.ResourceName]resource.Quantity{
		hv1.ResourceMemory: *resource.NewQuantity(vmMemoryBytes, resource.BinarySI),
		hv1.ResourceCPU:    *resource.NewQuantity(vmCPUs, resource.DecimalSI),
	}
	if retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.Reservation{}
		if err := c.Get(ctx, client.ObjectKey{Name: slotName}, latest); err != nil {
			return err
		}
		if latest.Spec.CommittedResourceReservation == nil {
			return fmt.Errorf("reservation %s lost CommittedResourceReservation spec", slotName)
		}
		base := latest.DeepCopy()
		if latest.Spec.CommittedResourceReservation.Allocations == nil {
			latest.Spec.CommittedResourceReservation.Allocations = make(map[string]v1alpha1.CommittedResourceAllocation)
		}
		latest.Spec.CommittedResourceReservation.Allocations[instanceUUID] = v1alpha1.CommittedResourceAllocation{
			CreationTimestamp: metav1.Now(),
			Resources:         vmResources,
		}
		return c.Patch(ctx, latest, client.MergeFrom(base))
	}); retryErr != nil {
		log.Error(retryErr, "CR allocation: failed to patch reservation",
			"reservation", slotName, "instanceUUID", instanceUUID)
		return
	}

	log.Info("CR allocation: done", "instanceUUID", instanceUUID, "reservation", slotName)
}

// classifyCRPlacement determines the CR slot outcome for a successful placement:
//   - no_cr: no active CR or CR capacity fully exhausted (H1)
//   - slot_missed: CR has remaining capacity but no candidate host has a slot with remaining > 0 (H2)
//   - slot_used: CR has remaining capacity and at least one candidate host has a slot with remaining > 0 (H3)
func classifyCRPlacement(
	evaluator *CRSlotEvaluator,
	activeCRs []v1alpha1.CommittedResource,
	candidateHosts []string,
	projectID, flavorGroupName string,
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
		return "no_cr"
	}
	for _, host := range candidateHosts {
		for _, slot := range evaluator.SlotsForHost(host, projectID, flavorGroupName) {
			if reservationRemainingMemory(slot) > 0 {
				return "slot_used"
			}
		}
	}
	return "slot_missed"
}

// pickReservationSlot selects the best reservation slot for a new VM.
// A slot is usable if it has any remaining memory (overfill is allowed: the VM
// may exceed the slot's remaining capacity, with the overflow covered by the
// host's free capacity which the pipeline already verified).
// Selection: maximise coverage (min(remMem, vmMemoryBytes)), tiebreak by
// smallest remaining memory (tightest fit), then reservation name.
// Returns the slot name, or "" if no slot has any remaining memory.
func pickReservationSlot(candidates []v1alpha1.Reservation, vmMemoryBytes int64) string {
	bestName := ""
	var bestCoverage, bestRemMem int64

	for _, res := range candidates {
		remMem := reservationRemainingMemory(res)
		if remMem <= 0 {
			continue
		}
		coverage := min(remMem, vmMemoryBytes)

		if bestName == "" ||
			coverage > bestCoverage ||
			(coverage == bestCoverage && remMem < bestRemMem) ||
			(coverage == bestCoverage && remMem == bestRemMem && res.Name < bestName) {
			bestName = res.Name
			bestCoverage = coverage
			bestRemMem = remMem
		}
	}

	return bestName
}

// reservationRemainingMemory returns how many bytes of memory remain
// unallocated in a reservation slot. Returns 0 if the slot is full or nil.
func reservationRemainingMemory(res v1alpha1.Reservation) int64 {
	cr := res.Spec.CommittedResourceReservation
	if cr == nil {
		return 0
	}
	totalMemQ := res.Spec.Resources[hv1.ResourceMemory]
	var usedMem int64
	for _, alloc := range cr.Allocations {
		allocMemQ := alloc.Resources[hv1.ResourceMemory]
		usedMem += allocMemQ.Value()
	}
	return max(totalMemQ.Value()-usedMem, 0)
}

// errFlavorNotInGroup is returned by resolveFlavorGroup when the flavor is not
// part of any configured flavor group (PAYG placement). Callers should
// distinguish this from real lookup errors.
var errFlavorNotInGroup = errors.New("flavor not in any group")

// resolveFlavorGroup looks up which flavor group the given flavor belongs to.
// Returns errFlavorNotInGroup (PAYG) if the flavor is not in any group.
// Returns a different error for transient failures (Knowledge CRD unavailable, etc).
func (c *FilterWeigherPipelineController) resolveFlavorGroup(ctx context.Context, flavorName string) (string, *compute.FlavorInGroup, error) {
	fgClient := reservations.FlavorGroupKnowledgeClient{Client: c.Client}
	flavorGroups, err := fgClient.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		return "", nil, err
	}
	groupName, flavor, err := reservations.FindFlavorInGroups(flavorName, flavorGroups)
	if err != nil {
		return "", nil, errFlavorNotInGroup
	}
	return groupName, flavor, nil
}
