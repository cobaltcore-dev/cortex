// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ReservationManager handles CRUD operations for Reservation CRDs.
type ReservationManager struct {
	client.Client
}

func NewReservationManager(k8sClient client.Client) *ReservationManager {
	return &ReservationManager{
		Client: k8sClient,
	}
}

// ApplyCommitmentState reconciles (CRUD) Reservation CRDs to match desired commitment state.
func (m *ReservationManager) ApplyCommitmentState(
	ctx context.Context,
	log logr.Logger,
	desiredState *CommitmentState,
	flavorGroups map[string]compute.FlavorGroupFeature,
	creator string,
) (touchedReservations, removedReservations []v1alpha1.Reservation, err error) {

	log = log.WithName("ReservationManager")

	existing, err := ListReservationsForCommitment(
		ctx, m.Client, desiredState.CommitmentUUID, "",
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list existing reservations: %w", err)
	}

	// Calculate the delta between existing and desired state
	flavorGroup, exists := flavorGroups[desiredState.FlavorGroupName]

	if !exists {
		return nil, nil, fmt.Errorf("flavor group not found: %s", desiredState.FlavorGroupName)
	}
	deltaMemoryBytes := desiredState.TotalMemoryBytes
	for _, res := range existing {
		memoryQuantity := res.Spec.Resources["memory"]
		deltaMemoryBytes -= memoryQuantity.Value()
	}

	log.Info("applying commitment state",
		"commitmentUUID", desiredState.CommitmentUUID,
		"desiredMemoryBytes", desiredState.TotalMemoryBytes,
		"deltaMemoryBytes", deltaMemoryBytes,
		"existingSlots", len(existing),
	)

	touchedReservations = make([]v1alpha1.Reservation, 0)
	removedReservations = make([]v1alpha1.Reservation, 0)
	nextSlotIndex := GetNextSlotIndex(existing)

	// check all reservations for flavor group/project consistency; on mismatch, delete reservation and re-create later
	validReservations := make([]v1alpha1.Reservation, 0)
	for _, res := range existing {
		if res.Spec.CommittedResourceReservation.ResourceGroup != desiredState.FlavorGroupName ||
			res.Spec.CommittedResourceReservation.ProjectID != desiredState.ProjectID {
			log.Info("Found a reservation with wrong flavor group or project, delete and recreate afterward",
				"commitmentUUID", desiredState.CommitmentUUID,
				"name", res.Name,
				"expectedFlavorGroup", desiredState.FlavorGroupName,
				"actualFlavorGroup", res.Spec.CommittedResourceReservation.ResourceGroup,
				"expectedProjectID", desiredState.ProjectID,
				"actualProjectID", res.Spec.CommittedResourceReservation.ProjectID)
			removedReservations = append(removedReservations, res)
			memValue := res.Spec.Resources["memory"]
			deltaMemoryBytes += memValue.Value()

			if err := m.Delete(ctx, &res); err != nil {
				return touchedReservations, removedReservations, fmt.Errorf("failed to delete reservation %s: %w", res.Name, err)
			}
		} else {
			validReservations = append(validReservations, res)
		}
	}
	existing = validReservations

	// remove reservations if needed (DELETE)
	for deltaMemoryBytes < 0 && len(existing) > 0 {
		// find next unused reservation slot or simply last one
		var reservationToDelete *v1alpha1.Reservation
		for i, res := range existing {
			if len(res.Spec.CommittedResourceReservation.Allocations) == 0 {
				reservationToDelete = &res
				existing = append(existing[:i], existing[i+1:]...) // remove from existing list
				break
			}
		}
		if reservationToDelete == nil {
			reservationToDelete = &existing[len(existing)-1]
			existing = existing[:len(existing)-1] // remove from existing list
		}
		removedReservations = append(removedReservations, *reservationToDelete)
		memValue := reservationToDelete.Spec.Resources["memory"]
		deltaMemoryBytes += memValue.Value()

		log.Info("deleting reservation",
			"commitmentUUID", desiredState.CommitmentUUID,
			"deltaMemoryBytes", deltaMemoryBytes,
			"name", reservationToDelete.Name,
			"numAllocations", len(reservationToDelete.Spec.CommittedResourceReservation.Allocations),
			"memoryBytes", memValue.Value())

		if err := m.Delete(ctx, reservationToDelete); err != nil {
			return touchedReservations, removedReservations, fmt.Errorf("failed to delete reservation %s: %w", reservationToDelete.Name, err)
		}
	}

	// Add new reservations if needed (CREATE)
	for deltaMemoryBytes > 0 {
		// Need to create new reservation slots
		reservation := m.buildReservationCRD(log, desiredState, nextSlotIndex, deltaMemoryBytes, flavorGroup, creator)
		touchedReservations = append(touchedReservations, *reservation)
		memValue := reservation.Spec.Resources["memory"]
		deltaMemoryBytes -= memValue.Value()

		log.Info("creating reservation",
			"commitmentUUID", desiredState.CommitmentUUID,
			"deltaMemoryBytes", deltaMemoryBytes,
			"name", reservation.Name,
			"memoryBytes", memValue.Value())

		if err := m.Create(ctx, reservation); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return touchedReservations, removedReservations, fmt.Errorf(
					"reservation %s already exists (collision detected): %w",
					reservation.Name, err)
			}
			return touchedReservations, removedReservations, fmt.Errorf(
				"failed to create reservation slot %d: %w",
				nextSlotIndex, err)
		}

		nextSlotIndex++
	}

	// Sync metadata for all remaining reservations
	for i := range existing {
		updated, err := m.syncReservationMetadata(ctx, log, &existing[i], desiredState)
		if err != nil {
			return touchedReservations, removedReservations, err
		}
		if updated != nil {
			touchedReservations = append(touchedReservations, *updated)
		}
	}

	log.Info("completed commitment state sync",
		"commitmentUUID", desiredState.CommitmentUUID,
		"totalReservations", len(existing),
		"created", len(touchedReservations)-len(existing),
		"deleted", len(removedReservations))

	return touchedReservations, removedReservations, nil
}

// syncReservationMetadata updates reservation metadata if it differs from desired state.
func (m *ReservationManager) syncReservationMetadata(
	ctx context.Context,
	log logr.Logger,
	reservation *v1alpha1.Reservation,
	state *CommitmentState,
) (*v1alpha1.Reservation, error) {

	// if any of AZ, StarTime, EndTime differ from desired state, need to patch
	if (state.AZ != "" && reservation.Spec.AZ != state.AZ) ||
		(state.StartTime != nil && (reservation.Spec.StartTime == nil || !reservation.Spec.StartTime.Time.Equal(*state.StartTime))) ||
		(state.EndTime != nil && (reservation.Spec.EndTime == nil || !reservation.Spec.EndTime.Time.Equal(*state.EndTime))) {
		// Apply patch
		log.Info("syncing reservation metadata",
			"reservation", reservation.Name,
			"az", state.AZ,
			"startTime", state.StartTime,
			"endTime", state.EndTime)

		patch := client.MergeFrom(reservation.DeepCopy())

		if state.AZ != "" {
			reservation.Spec.AZ = state.AZ
		}
		if state.StartTime != nil {
			reservation.Spec.StartTime = &metav1.Time{Time: *state.StartTime}
		}
		if state.EndTime != nil {
			reservation.Spec.EndTime = &metav1.Time{Time: *state.EndTime}
		}

		if err := m.Patch(ctx, reservation, patch); err != nil {
			return nil, fmt.Errorf("failed to patch reservation %s: %w",
				reservation.Name, err)
		}

		return reservation, nil
	} else {
		return nil, nil // No changes needed
	}
}

func (m *ReservationManager) buildReservationCRD(
	log logr.Logger,
	state *CommitmentState,
	slotIndex int,
	deltaMemoryBytes int64,
	flavorGroup compute.FlavorGroupFeature,
	creator string,
) *v1alpha1.Reservation {

	name := fmt.Sprintf("commitment-%s-%d", state.CommitmentUUID, slotIndex)

	// Select first flavor that fits remaining memory (flavors sorted descending by size)
	flavorInGroup := flavorGroup.Flavors[len(flavorGroup.Flavors)-1] // default to smallest
	memoryBytes := deltaMemoryBytes
	cpus := int64(flavorInGroup.VCPUs) //nolint:gosec // VCPUs from flavor specs, realistically bounded

	for _, flavor := range flavorGroup.Flavors {
		flavorMemoryBytes := int64(flavor.MemoryMB) * 1024 * 1024 //nolint:gosec // flavor memory from specs, realistically bounded
		if flavorMemoryBytes <= deltaMemoryBytes {
			flavorInGroup = flavor
			memoryBytes = flavorMemoryBytes
			cpus = int64(flavorInGroup.VCPUs) //nolint:gosec // VCPUs from flavor specs, realistically bounded
			break
		}
	}

	log.Info("created reservation", "name", name, "slot", slotIndex, "flavor", flavorInGroup.Name, "memoryBytes", memoryBytes, "flavor memory", flavorInGroup.MemoryMB*1024*1024)

	spec := v1alpha1.ReservationSpec{
		Type: v1alpha1.ReservationTypeCommittedResource,
		Resources: map[string]resource.Quantity{
			"memory": *resource.NewQuantity(
				memoryBytes,
				resource.BinarySI,
			),
			"cpu": *resource.NewQuantity(
				cpus,
				resource.DecimalSI,
			),
		},
		CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
			ProjectID:     state.ProjectID,
			DomainID:      state.DomainID,
			ResourceGroup: state.FlavorGroupName,
			ResourceName:  flavorInGroup.Name,
			Creator:       creator,
			Allocations:   make(map[string]v1alpha1.CommittedResourceAllocation),
		},
	}

	// Set AZ if specified
	if state.AZ != "" {
		spec.AZ = state.AZ
	}

	// Set validity times if specified
	if state.StartTime != nil {
		spec.StartTime = &metav1.Time{Time: *state.StartTime}
	}
	if state.EndTime != nil {
		spec.EndTime = &metav1.Time{Time: *state.EndTime}
	}

	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: spec,
	}
}
