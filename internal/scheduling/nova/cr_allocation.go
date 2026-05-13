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
// Best-effort: any failure is logged but never propagated to the caller.
func (c *FilterWeigherPipelineController) recordCRAllocation(ctx context.Context, decision *v1alpha1.Decision, request api.ExternalSchedulerRequest) {
	log := ctrl.LoggerFrom(ctx)

	instanceUUID := request.Spec.Data.InstanceUUID
	projectID := request.Context.ProjectID
	flavorName := request.Spec.Data.Flavor.Data.Name
	selectedHost := *decision.Status.Result.TargetHost

	flavorGroupName, flavorInGroup, err := c.resolveFlavorGroup(ctx, flavorName)
	if err != nil {
		if errors.Is(err, errFlavorNotInGroup) {
			log.V(1).Info("CR allocation: flavor not in any group, PAYG placement", "flavor", flavorName)
		} else {
			log.Error(err, "CR allocation: failed to resolve flavor group",
				"flavor", flavorName, "instanceUUID", instanceUUID)
		}
		return
	}

	// List all CR reservations and filter to candidates matching this placement.
	var reservationList v1alpha1.ReservationList
	if err := c.List(ctx, &reservationList,
		client.MatchingLabels{v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource},
	); err != nil {
		log.Error(err, "CR allocation: failed to list reservations", "instanceUUID", instanceUUID)
		return
	}

	var candidates []v1alpha1.Reservation
	for _, res := range reservationList.Items {
		cr := res.Spec.CommittedResourceReservation
		if cr == nil {
			continue
		}
		if res.Spec.TargetHost != selectedHost || cr.ProjectID != projectID || cr.ResourceGroup != flavorGroupName {
			continue
		}
		// Idempotency: if this VM UUID is already recorded, the work is done.
		if _, exists := cr.Allocations[instanceUUID]; exists {
			log.Info("CR allocation: VM UUID already in reservation, skipping",
				"instanceUUID", instanceUUID, "reservation", res.Name)
			return
		}
		candidates = append(candidates, res)
	}

	if len(candidates) == 0 {
		log.V(1).Info("CR allocation: no matching reservation slot, PAYG placement",
			"instanceUUID", instanceUUID, "host", selectedHost,
			"projectID", projectID, "flavorGroup", flavorGroupName)
		return
	}

	vmMemoryBytes := int64(flavorInGroup.MemoryMB) * 1024 * 1024 //nolint:gosec // flavor memory bounded by specs
	vmCPUs := int64(flavorInGroup.VCPUs)                         //nolint:gosec // VCPUs bounded by specs

	slotName := pickReservationSlot(candidates, vmMemoryBytes, vmCPUs)
	if slotName == "" {
		log.Error(nil, "CR allocation: no reservation slot has sufficient remaining capacity",
			"instanceUUID", instanceUUID, "vmMemoryBytes", vmMemoryBytes,
			"host", selectedHost, "candidates", len(candidates))
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
		if latest.Spec.CommittedResourceReservation.Allocations == nil {
			latest.Spec.CommittedResourceReservation.Allocations = make(map[string]v1alpha1.CommittedResourceAllocation)
		}
		latest.Spec.CommittedResourceReservation.Allocations[instanceUUID] = v1alpha1.CommittedResourceAllocation{
			CreationTimestamp: metav1.Now(),
			Resources:         vmResources,
		}
		return c.Update(ctx, latest)
	}); retryErr != nil {
		log.Error(retryErr, "CR allocation: failed to patch reservation",
			"reservation", slotName, "instanceUUID", instanceUUID)
		return
	}

	log.Info("CR allocation: done", "instanceUUID", instanceUUID, "reservation", slotName)
}

// pickReservationSlot selects the reservation slot with the least remaining
// memory that can still fully fit vmMemoryBytes and vmCPUs.
// Tiebreaks: least remaining CPU, then reservation name (lexicographic).
// Returns the slot name, or "" if no slot fits.
func pickReservationSlot(candidates []v1alpha1.Reservation, vmMemoryBytes, vmCPUs int64) string {
	bestName := ""
	var bestRemMem, bestRemCPU int64

	for _, res := range candidates {
		cr := res.Spec.CommittedResourceReservation

		totalCPUQ := res.Spec.Resources[hv1.ResourceCPU]
		totalCPU := totalCPUQ.Value()

		var usedCPU int64
		for _, alloc := range cr.Allocations {
			cpuQ := alloc.Resources[hv1.ResourceCPU]
			usedCPU += cpuQ.Value()
		}

		remMem := reservationRemainingMemory(res)
		remCPU := max(totalCPU-usedCPU, 0)

		if remMem < vmMemoryBytes || remCPU < vmCPUs {
			continue // Slot doesn't have enough remaining capacity.
		}

		if bestName == "" ||
			remMem < bestRemMem ||
			(remMem == bestRemMem && remCPU < bestRemCPU) ||
			(remMem == bestRemMem && remCPU == bestRemCPU && res.Name < bestName) {
			bestName = res.Name
			bestRemMem = remMem
			bestRemCPU = remCPU
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
