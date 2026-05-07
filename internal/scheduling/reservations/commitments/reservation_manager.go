// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ApplyResult contains the result of applying a commitment state.
type ApplyResult struct {
	// Created is the number of reservations created
	Created int
	// Deleted is the number of reservations deleted
	Deleted int
	// Repaired is the number of reservations repaired (metadata sync or recreated due to wrong config)
	Repaired int
	// TotalSlots is the total number of reservation slots that should exist after the apply.
	// Used by the CR controller to wait for the correct number of children in the cache.
	TotalSlots int
	// TouchedReservations are reservations that were created or updated
	TouchedReservations []v1alpha1.Reservation
	// RemovedReservations are reservations that were deleted
	RemovedReservations []v1alpha1.Reservation
}

// ReservationManager handles CRUD operations for Reservation CRDs.
type ReservationManager struct {
	client.Client
}

func NewReservationManager(k8sClient client.Client) *ReservationManager {
	return &ReservationManager{
		Client: k8sClient,
	}
}

// ApplyCommitmentState synchronizes Reservation CRDs to match the desired commitment state.
// This function performs CRUD operations (create/update/delete) on reservation slots to align
// with the capacity specified in desiredState.
//
// Entry points:
//   - from Syncer - periodic sync with Limes state
//   - from API ChangeCommitmentsHandler - batch processing of commitment changes
//
// The function is idempotent and handles:
//   - Repairing inconsistent slots (wrong flavor group/project)
//   - Creating new reservation slots when capacity increases
//   - Deleting unused/excess slots when capacity decreases
//   - Syncing reservation metadata for all remaining slots
//
// Returns ApplyResult containing touched/removed reservations and counts for metrics.
func (m *ReservationManager) ApplyCommitmentState(
	ctx context.Context,
	log logr.Logger,
	desiredState *CommitmentState,
	flavorGroups map[string]compute.FlavorGroupFeature,
	creator string,
) (*ApplyResult, error) {

	result := &ApplyResult{}

	log = log.WithName("ReservationManager")

	// Phase 1: List and filter existing reservations for this commitment
	var allReservations v1alpha1.ReservationList
	if err := m.List(ctx, &allReservations, client.MatchingLabels{
		v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
	}); err != nil {
		return nil, fmt.Errorf("failed to list reservations: %w", err)
	}

	// Filter by CommitmentUUID to find reservations for this commitment
	var existing []v1alpha1.Reservation
	for _, res := range allReservations.Items {
		if res.Spec.CommittedResourceReservation != nil &&
			res.Spec.CommittedResourceReservation.CommitmentUUID == desiredState.CommitmentUUID {
			existing = append(existing, res)
		}
	}

	// Phase 2: Calculate memory delta (desired - current)
	flavorGroup, exists := flavorGroups[desiredState.FlavorGroupName]

	if !exists {
		return nil, fmt.Errorf("flavor group not found: %s", desiredState.FlavorGroupName)
	}
	if len(flavorGroup.Flavors) == 0 {
		return nil, fmt.Errorf("flavor group %s has no flavors", desiredState.FlavorGroupName)
	}
	deltaMemoryBytes := desiredState.TotalMemoryBytes
	for _, res := range existing {
		memoryQuantity := res.Spec.Resources[hv1.ResourceMemory]
		deltaMemoryBytes -= memoryQuantity.Value()
	}

	// Log only if there's actual work to do (delta != 0)
	hasChanges := deltaMemoryBytes != 0

	nextSlotIndex := GetNextSlotIndex(existing)

	// Phase 3 (DELETE): Delete inconsistent reservations (wrong flavor group/project)
	// They will be recreated with correct metadata in subsequent phases.
	var validReservations []v1alpha1.Reservation
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
			result.Repaired++
			result.RemovedReservations = append(result.RemovedReservations, res)
			memValue := res.Spec.Resources[hv1.ResourceMemory]
			deltaMemoryBytes += memValue.Value()

			if err := m.Delete(ctx, &res); err != nil {
				return result, fmt.Errorf("failed to delete reservation %s: %w", res.Name, err)
			}
		} else {
			validReservations = append(validReservations, res)
		}
	}
	existing = validReservations

	// Phase 4 (DELETE): Remove reservations (capacity decreased)
	for deltaMemoryBytes < 0 && len(existing) > 0 {
		// prefer ones that are not scheduled, or alternatively, unused reservation slot, or simply remove last one
		var reservationToDelete *v1alpha1.Reservation
		for i, res := range existing {
			if res.Spec.TargetHost == "" {
				reservationToDelete = &res
				existing = append(existing[:i], existing[i+1:]...) // remove from existing list
				break
			}
		}
		if reservationToDelete == nil {
			for i, res := range existing {
				if len(res.Spec.CommittedResourceReservation.Allocations) == 0 {
					reservationToDelete = &res
					existing = append(existing[:i], existing[i+1:]...) // remove from existing list
					break
				}
			}
		}
		if reservationToDelete == nil {
			reservationToDelete = &existing[len(existing)-1]
			existing = existing[:len(existing)-1] // remove from existing list
		}
		result.RemovedReservations = append(result.RemovedReservations, *reservationToDelete)
		result.Deleted++
		memValue := reservationToDelete.Spec.Resources[hv1.ResourceMemory]
		deltaMemoryBytes += memValue.Value()

		if err := m.Delete(ctx, reservationToDelete); err != nil {
			return result, fmt.Errorf("failed to delete reservation %s: %w", reservationToDelete.Name, err)
		}
	}

	// Phase 5 (CREATE): Create new reservations (capacity increased)
	for deltaMemoryBytes > 0 {
		// Select the largest flavor that fits the remaining delta (flavors sorted descending by memory).
		reservation := m.newReservation(desiredState, nextSlotIndex, deltaMemoryBytes, flavorGroup, creator)
		result.TouchedReservations = append(result.TouchedReservations, *reservation)
		memValue := reservation.Spec.Resources[hv1.ResourceMemory]
		deltaMemoryBytes -= memValue.Value()
		result.Created++

		if err := m.Create(ctx, reservation); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return result, fmt.Errorf(
					"reservation %s already exists (collision detected): %w",
					reservation.Name, err)
			}
			return result, fmt.Errorf(
				"failed to create reservation slot %d: %w",
				nextSlotIndex, err)
		}

		nextSlotIndex++
	}

	// Phase 6 (UPDATE): Sync metadata for remaining reservations
	for i := range existing {
		updated, err := m.syncReservationMetadata(ctx, log, &existing[i], desiredState)
		if err != nil {
			return result, err
		}
		if updated != nil {
			result.TouchedReservations = append(result.TouchedReservations, *updated)
			result.Repaired++
		}
	}

	// Only log if there were actual changes
	if hasChanges || result.Created > 0 || len(result.RemovedReservations) > 0 || result.Repaired > 0 {
		log.Info("commitment state sync completed",
			"commitmentUUID", desiredState.CommitmentUUID,
			"created", result.Created,
			"deleted", result.Deleted,
			"repaired", result.Repaired,
			"total", len(existing)+result.Created)
	}

	result.TotalSlots = len(existing) + result.Created
	return result, nil
}

// syncReservationMetadata updates reservation metadata if it differs from desired state.
func (m *ReservationManager) syncReservationMetadata(
	ctx context.Context,
	logger logr.Logger,
	reservation *v1alpha1.Reservation,
	state *CommitmentState,
) (*v1alpha1.Reservation, error) {

	// if any of CommitmentUUID, AZ, StarTime, EndTime differ from desired state, need to patch
	if (state.CommitmentUUID != "" && reservation.Spec.CommittedResourceReservation.CommitmentUUID != state.CommitmentUUID) ||
		(state.AvailabilityZone != "" && reservation.Spec.AvailabilityZone != state.AvailabilityZone) ||
		(state.StartTime != nil && (reservation.Spec.StartTime == nil || !reservation.Spec.StartTime.Time.Equal(*state.StartTime))) ||
		(state.EndTime != nil && (reservation.Spec.EndTime == nil || !reservation.Spec.EndTime.Time.Equal(*state.EndTime))) ||
		(state.ParentGeneration != 0 && reservation.Spec.CommittedResourceReservation.ParentGeneration != state.ParentGeneration) {
		// Apply patch
		logger.V(1).Info("syncing reservation metadata",
			"reservation", reservation.Name,
			"commitmentUUID", state.CommitmentUUID)

		patch := client.MergeFrom(reservation.DeepCopy())

		if state.CommitmentUUID != "" {
			reservation.Spec.CommittedResourceReservation.CommitmentUUID = state.CommitmentUUID
		}
		if state.ParentGeneration != 0 {
			reservation.Spec.CommittedResourceReservation.ParentGeneration = state.ParentGeneration
		}

		if state.AvailabilityZone != "" {
			reservation.Spec.AvailabilityZone = state.AvailabilityZone
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

func (m *ReservationManager) newReservation(
	state *CommitmentState,
	slotIndex int,
	deltaMemoryBytes int64,
	flavorGroup compute.FlavorGroupFeature,
	creator string,
) *v1alpha1.Reservation {

	namePrefix := state.NamePrefix
	if namePrefix == "" {
		namePrefix = fmt.Sprintf("commitment-%s-", state.CommitmentUUID)
	}
	name := fmt.Sprintf("%s%d", namePrefix, slotIndex)

	// Select largest flavor that fits remaining memory (flavors sorted descending by memory then vCPUs).
	// This works for both fixed and varying CPU:RAM ratio groups.
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

	spec := v1alpha1.ReservationSpec{
		Type:             v1alpha1.ReservationTypeCommittedResource,
		SchedulingDomain: v1alpha1.SchedulingDomainNova,
		Resources: map[hv1.ResourceName]resource.Quantity{
			hv1.ResourceMemory: *resource.NewQuantity(
				memoryBytes,
				resource.BinarySI,
			),
			hv1.ResourceCPU: *resource.NewQuantity(
				cpus,
				resource.DecimalSI,
			),
		},
		CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
			ProjectID:        state.ProjectID,
			CommitmentUUID:   state.CommitmentUUID,
			DomainID:         state.DomainID,
			ResourceGroup:    state.FlavorGroupName,
			ResourceName:     flavorInGroup.Name,
			Creator:          creator,
			ParentGeneration: state.ParentGeneration,
			Allocations:      nil,
		},
	}

	// Set AvailabilityZone if specified
	if state.AvailabilityZone != "" {
		spec.AvailabilityZone = state.AvailabilityZone
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
			Labels: map[string]string{
				v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
			},
			Annotations: map[string]string{
				v1alpha1.AnnotationCreatorRequestID: state.CreatorRequestID,
			},
		},
		Spec: spec,
	}
}
