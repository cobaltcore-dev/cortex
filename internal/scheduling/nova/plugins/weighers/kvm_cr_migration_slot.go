// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

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

// Options for the KVM CR migration slot weigher.
type KVMCRMigrationSlotOpts struct {
	// Weight assigned to hosts that have a compatible CR reservation slot.
	// Default: 1.0
	SlotHostWeight *float64 `json:"slotHostWeight,omitempty"`
	// Weight assigned to all other hosts when a source slot is found.
	// Default: 0.1
	DefaultHostWeight *float64 `json:"defaultHostWeight,omitempty"`
}

func (o KVMCRMigrationSlotOpts) Validate() error {
	return nil
}

func (o KVMCRMigrationSlotOpts) GetSlotHostWeight() float64 {
	if o.SlotHostWeight == nil {
		return 0.1
	}
	return *o.SlotHostWeight
}

func (o KVMCRMigrationSlotOpts) GetDefaultHostWeight() float64 {
	if o.DefaultHostWeight == nil {
		return 0.0
	}
	return *o.DefaultHostWeight
}

// KVMCRMigrationSlotStep weighs live-migration candidates by whether they can
// accommodate the CR reservation slot of the migrating VM.
//
// When a VM with a CR reservation slot is migrated, this weigher boosts hosts
// that have a ready CR reservation with sufficient remaining capacity for the
// slot (not just the VM flavor). This steers the migration toward hosts where
// the reservation can follow the VM, minimising the double-blocking window.
//
// If the VM has no CR reservation, or no candidate can accommodate the slot,
// all candidates receive zero weight (no effect on ranking).
//
// Only activates for LiveMigrationIntent.
type KVMCRMigrationSlotStep struct {
	lib.BaseWeigher[api.ExternalSchedulerRequest, KVMCRMigrationSlotOpts]
}

func (s *KVMCRMigrationSlotStep) Run(
	traceLog *slog.Logger,
	request api.ExternalSchedulerRequest,
) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)

	intent, err := request.GetIntent()
	if err != nil || intent != api.LiveMigrationIntent {
		traceLog.Info("not a live migration, skipping CR migration slot weigher")
		return result, nil //nolint:nilerr
	}

	instanceUUID := request.Spec.Data.InstanceUUID
	projectID := request.Spec.Data.ProjectID

	var allReservations v1alpha1.ReservationList
	if err := s.Client.List(context.Background(), &allReservations,
		client.MatchingLabels{v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource},
	); err != nil {
		return nil, err
	}

	// Find the source slot that has this VM confirmed.
	var sourceSlot *v1alpha1.Reservation
	for i := range allReservations.Items {
		res := &allReservations.Items[i]
		if res.Status.CommittedResourceReservation == nil {
			continue
		}
		if _, ok := res.Status.CommittedResourceReservation.Allocations[instanceUUID]; ok {
			sourceSlot = res
			break
		}
	}

	if sourceSlot == nil {
		traceLog.Info("migrating VM has no confirmed CR reservation slot, skipping slot weigher",
			"instanceUUID", instanceUUID)
		CRMigrationSlotMetricsSingleton.RecordResult("no_source_slot")
		return result, nil
	}

	slotMemoryBytes := sourceSlot.Spec.Resources[hv1.ResourceMemory]
	if slotMemoryBytes.IsZero() {
		traceLog.Info("source CR slot has no memory resource, skipping slot weigher",
			"instanceUUID", instanceUUID,
			"reservation", sourceSlot.Name)
		CRMigrationSlotMetricsSingleton.RecordResult("no_source_slot")
		return result, nil
	}

	resourceGroup := sourceSlot.Spec.CommittedResourceReservation.ResourceGroup

	traceLog.Info("found source CR reservation slot for migrating VM",
		"instanceUUID", instanceUUID,
		"reservation", sourceSlot.Name,
		"slotMemoryBytes", slotMemoryBytes.Value(),
		"resourceGroup", resourceGroup,
	)

	evaluator, err := crs.BuildSlotEvaluatorFromReservations(context.Background(), s.Client, allReservations.Items)
	if err != nil {
		return nil, err
	}

	slotHostWeight := s.Options.GetSlotHostWeight()
	defaultHostWeight := s.Options.GetDefaultHostWeight()

	slotFound := false
	for host := range result.Activations {
		hasSlot := evaluator.HasSlotWithCapacity(host, projectID, resourceGroup, slotMemoryBytes.Value())
		canFit := evaluator.CanAccommodateSlot(host, slotMemoryBytes.Value())
		if hasSlot {
			result.Activations[host] = slotHostWeight
			slotFound = true
			traceLog.Info("host has existing CR slot for migration, boosting weight",
				"host", host, "weight", slotHostWeight)
		} else if canFit {
			result.Activations[host] = slotHostWeight
			traceLog.Info("host can accommodate slot via reconciler, boosting weight",
				"host", host, "weight", slotHostWeight)
		} else {
			result.Activations[host] = defaultHostWeight
			traceLog.Info("host cannot accommodate CR slot, applying low weight",
				"host", host, "weight", defaultHostWeight)
		}
	}

	if slotFound {
		CRMigrationSlotMetricsSingleton.RecordResult("slot_found")
	} else {
		CRMigrationSlotMetricsSingleton.RecordResult("no_slot")
	}

	return result, nil
}

func init() {
	Index["kvm_cr_migration_slot"] = func() NovaWeigher {
		return &KVMCRMigrationSlotStep{}
	}
}
