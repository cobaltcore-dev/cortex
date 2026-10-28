// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package crs

import (
	"context"
	"errors"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/prometheus/client_golang/prometheus"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Recorder receives scheduling outcomes and updates CR reservations and metrics accordingly.
type Recorder struct {
	// Client is the Kubernetes client used to read and patch Reservation CRDs.
	client.Client

	// NoHostFoundCounter counts no-host-found results by CR slot outcome.
	NoHostFoundCounter *prometheus.CounterVec
	// PlacementCounter counts successful placements by CR slot outcome.
	PlacementCounter *prometheus.CounterVec
}

// errFlavorNotInGroup is returned by resolveFlavorGroup when the flavor is not
// part of any configured flavor group (PAYG placement). Callers should
// distinguish this from real lookup errors.
var errFlavorNotInGroup = errors.New("flavor not in any group")

// resolveFlavorGroup looks up which flavor group the given flavor belongs to.
// Returns errFlavorNotInGroup (PAYG) if the flavor is not in any group.
// Returns a different error for transient failures (Knowledge CRD unavailable, etc).
func (r *Recorder) resolveFlavorGroup(ctx context.Context, flavorName string) (string, *compute.FlavorInGroup, error) {
	fgClient := reservations.FlavorGroupKnowledgeClient{Client: r.Client}
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

// RecordPlacement writes the placed VM UUID into the matching Reservation slot
// and emits placement metrics. Called after a successful Nova placement.
func (r *Recorder) RecordPlacement(ctx context.Context, decision *v1alpha1.Decision, request api.ExternalSchedulerRequest) {
	log := ctrl.LoggerFrom(ctx)

	instanceUUID := request.Spec.Data.InstanceUUID
	projectID := request.Context.ProjectID
	flavorName := request.Spec.Data.Flavor.Data.Name
	selectedHost := *decision.Status.Result.TargetHost
	intent := string(decision.Spec.Intent)

	flavorGroupName, flavorInGroup, err := r.resolveFlavorGroup(ctx, flavorName)
	if err != nil {
		if errors.Is(err, errFlavorNotInGroup) {
			log.V(1).Info("CR allocation: flavor not in any group, PAYG placement", "flavor", flavorName)
		} else {
			log.Error(err, "CR allocation: failed to resolve flavor group",
				"flavor", flavorName, "instanceUUID", instanceUUID)
			if r.PlacementCounter != nil {
				r.PlacementCounter.WithLabelValues("unknown", intent, "error").Inc()
			}
		}
		return
	}

	evaluator, err := BuildSlotEvaluator(ctx, r.Client)
	if err != nil {
		log.Error(err, "CR allocation: failed to build CR slot evaluator", "instanceUUID", instanceUUID)
		return
	}

	var crList v1alpha1.CommittedResourceList
	if err := r.List(ctx, &crList); err != nil {
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
	crOutcome := ClassifyPlacement(evaluator, activeCRs, candidateHosts, projectID, flavorGroupName)

	log.V(1).Info("CR allocation: placement classified",
		"cr_outcome", crOutcome,
		"instanceUUID", instanceUUID,
		"host", selectedHost,
		"projectID", projectID,
		"flavorGroup", flavorGroupName,
	)
	if r.PlacementCounter != nil {
		r.PlacementCounter.WithLabelValues(flavorGroupName, intent, crOutcome).Inc()
	}

	if crOutcome != "slot_used" {
		return
	}

	slotsOnTarget := evaluator.SlotsForHost(selectedHost, projectID, flavorGroupName)

	for _, slot := range slotsOnTarget {
		if slot.Status.CommittedResourceReservation != nil {
			if _, exists := slot.Status.CommittedResourceReservation.Allocations[instanceUUID]; exists {
				log.V(1).Info("CR allocation: VM UUID already confirmed in reservation, skipping",
					"instanceUUID", instanceUUID, "reservation", slot.Name)
				return
			}
		}
	}

	vmMemoryBytes := int64(flavorInGroup.MemoryMB) * 1024 * 1024 //nolint:gosec // flavor memory bounded by specs

	slotName := PickSlot(slotsOnTarget, vmMemoryBytes)
	if slotName == "" {
		log.V(1).Info("CR allocation: slot_used but target host has no slot with remaining capacity",
			"instanceUUID", instanceUUID, "host", selectedHost)
		return
	}

	var slot *v1alpha1.Reservation
	for i := range slotsOnTarget {
		if slotsOnTarget[i].Name == slotName {
			slot = &slotsOnTarget[i]
			break
		}
	}
	if slot == nil {
		log.V(1).Info("CR allocation: picked slot not found in slotsOnTarget (race?)",
			"instanceUUID", instanceUUID, "reservation", slotName)
		return
	}

	old := slot.DeepCopy()
	if slot.Annotations == nil {
		slot.Annotations = make(map[string]string)
	}
	slot.Annotations[v1alpha1.AnnotationCandidateAllocation] = instanceUUID
	if err := r.Patch(ctx, slot, client.MergeFrom(old)); err != nil {
		log.Error(err, "CR allocation: failed to annotate reservation with candidate VM UUID; controller will confirm via HV CRD",
			"instanceUUID", instanceUUID, "reservation", slotName)
	} else {
		log.V(1).Info("CR allocation: reservation annotated with candidate VM UUID",
			"instanceUUID", instanceUUID, "reservation", slotName)
	}
}

// RecordNoHostFound classifies a no-host-found result and emits a log line and metric.
func (r *Recorder) RecordNoHostFound(ctx context.Context, decision *v1alpha1.Decision, request api.ExternalSchedulerRequest) {
	log := ctrl.LoggerFrom(ctx)

	projectID := request.Context.ProjectID
	flavorName := request.Spec.Data.Flavor.Data.Name
	instanceUUID := request.Spec.Data.InstanceUUID
	intent := decision.Spec.Intent

	flavorGroupName, flavorInGroup, err := r.resolveFlavorGroup(ctx, flavorName)
	if err != nil {
		if errors.Is(err, errFlavorNotInGroup) {
			return // PAYG: flavor has no group, no metric
		}
		log.Error(err, "no-host-found: failed to resolve flavor group",
			"instanceUUID", instanceUUID, "flavor", flavorName)
		if r.NoHostFoundCounter != nil {
			r.NoHostFoundCounter.WithLabelValues("error", "unknown", string(intent)).Inc()
		}
		return
	}

	vmMemBytes := int64(flavorInGroup.MemoryMB) * 1024 * 1024 //nolint:gosec // flavor memory bounded by specs

	var crList v1alpha1.CommittedResourceList
	if err := r.List(ctx, &crList); err != nil {
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

	evaluator, err := BuildSlotEvaluator(ctx, r.Client)
	if err != nil {
		log.Error(err, "no-host-found: failed to build CR slot evaluator", "instanceUUID", instanceUUID)
		return
	}

	noHostFoundCase := ClassifyNoHostFound(activeCRs, evaluator, request.GetHosts(), projectID, flavorGroupName, vmMemBytes)

	log.Info("no-host-found classified",
		"cr_slot", noHostFoundCase,
		"instanceUUID", instanceUUID,
		"projectID", projectID,
		"flavorGroup", flavorGroupName,
		"intent", intent,
	)
	if r.NoHostFoundCounter != nil {
		r.NoHostFoundCounter.WithLabelValues(noHostFoundCase, flavorGroupName, string(intent)).Inc()
	}
}
