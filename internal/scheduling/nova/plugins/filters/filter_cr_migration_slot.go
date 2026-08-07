// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"

	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/crs"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

// FilterCRMigrationSlotStep filters live-migration candidates by committed-resource
// slot size rather than VM flavor size.
//
// When a VM that occupies a CR reservation slot is live-migrated, the target host
// must accommodate the full slot, not just the VM's flavor resources. This filter
// removes candidates that lack a ready CR reservation with sufficient remaining
// capacity for the slot.
//
// Placement order in the pipeline: last filter, after all other filters have run.
// Fallback: if no candidate survives the slot-size check, the original candidate
// set is returned unchanged so that the VM can still migrate using flavor-sized
// capacity on the target host.
//
// Only activates for LiveMigrationIntent. All other intents pass through unchanged.
type FilterCRMigrationSlotStep struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, lib.EmptyFilterWeigherPipelineStepOpts]
}

func (s *FilterCRMigrationSlotStep) Run(
	traceLog *slog.Logger,
	request api.ExternalSchedulerRequest,
) (*lib.FilterWeigherPipelineStepResult, error) {

	result := s.IncludeAllHostsFromRequest(request)

	intent, err := request.GetIntent()
	if err != nil || intent != api.LiveMigrationIntent {
		traceLog.Info("not a live migration, skipping CR slot filter")
		return result, nil //nolint:nilerr
	}

	instanceUUID := request.Spec.Data.InstanceUUID
	projectID := request.Spec.Data.ProjectID

	// List all CR reservations once. We reuse this list for both finding the
	// source slot and building the slot evaluator for target hosts, avoiding
	// a second K8s read inside BuildSlotEvaluator.
	var allReservations v1alpha1.ReservationList
	if err := s.Client.List(context.Background(), &allReservations,
		client.MatchingLabels{v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource},
	); err != nil {
		return nil, err
	}

	// Find the source reservation that currently holds this VM UUID (confirmed).
	var sourceSlot *v1alpha1.Reservation
	for i := range allReservations.Items {
		res := &allReservations.Items[i]
		if res.Status.CommittedResourceReservation == nil {
			continue
		}
		if _, confirmed := res.Status.CommittedResourceReservation.Allocations[instanceUUID]; confirmed {
			sourceSlot = res
			break
		}
	}

	if sourceSlot == nil {
		traceLog.Info("migrating VM has no confirmed CR reservation slot, skipping slot filter",
			"instanceUUID", instanceUUID)
		return result, nil
	}

	slotMemoryBytes := sourceSlot.Spec.Resources[hv1.ResourceMemory]
	if slotMemoryBytes.IsZero() {
		traceLog.Info("source CR slot has no memory resource, skipping slot filter",
			"instanceUUID", instanceUUID,
			"reservation", sourceSlot.Name)
		return result, nil
	}

	resourceGroup := sourceSlot.Spec.CommittedResourceReservation.ResourceGroup

	traceLog.Info("found source CR reservation slot for migrating VM",
		"instanceUUID", instanceUUID,
		"reservation", sourceSlot.Name,
		"slotMemoryBytes", slotMemoryBytes.Value(),
		"resourceGroup", resourceGroup,
	)

	// Build the slot evaluator from the already-fetched reservation list so we
	// don't issue a second List call. HVs are still fetched once inside the evaluator.
	evaluator, err := crs.BuildSlotEvaluatorFromReservations(context.Background(), s.Client, allReservations.Items)
	if err != nil {
		return nil, err
	}

	// Filter candidates to those with a ready CR slot that has at least slotMemoryBytes
	// remaining. This is a strict check — no overfill model — because the slot must
	// fully migrate with the VM.
	filtered := make(map[string]float64, len(result.Activations))
	for host := range result.Activations {
		if evaluator.HasSlotWithCapacity(host, projectID, resourceGroup, slotMemoryBytes.Value()) {
			filtered[host] = result.Activations[host]
			traceLog.Info("host has usable CR slot for migration",
				"host", host, "slotMemoryBytes", slotMemoryBytes.Value())
		} else {
			traceLog.Info("host has no usable CR slot for migration slot size, excluding",
				"host", host, "slotMemoryBytes", slotMemoryBytes.Value())
		}
	}

	// Fallback: if no host has a matching slot, return all candidates so the VM
	// can still migrate using regular (non-slot) capacity.
	if len(filtered) == 0 {
		traceLog.Info("no hosts with matching CR slot found, falling back to all candidates",
			"instanceUUID", instanceUUID,
			"slotMemoryBytes", slotMemoryBytes.Value(),
			"candidateCount", len(result.Activations),
		)
		return result, nil
	}

	result.Activations = filtered
	return result, nil
}

func init() {
	Index["filter_cr_migration_slot"] = func() NovaFilter {
		return &FilterCRMigrationSlotStep{}
	}
}
