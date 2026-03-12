// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/go-logr/logr"
	. "github.com/majewsky/gg/option"
	"github.com/sapcc/go-api-declarations/liquid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// watchTimeout is how long to wait for all reservations to become ready
	watchTimeout = 20 * time.Second

	// pollInterval is how frequently to poll reservation status
	pollInterval = 1 * time.Second
)

// implements POST /v1/change-commitments from Limes LIQUID API:
// See: https://github.com/sapcc/go-api-declarations/blob/main/liquid/commitment.go
// See: https://pkg.go.dev/github.com/sapcc/go-api-declarations/liquid
//
// This endpoint handles commitment changes by creating/updating/deleting Reservation CRDs based on the commitment lifecycle.
// A request may contain multiple commitment changes which are processed in a single transaction. If any change fails, all changes are rolled back.
func (api *HTTPAPI) HandleChangeCommitments(w http.ResponseWriter, r *http.Request) {
	// Serialize all change-commitments requests
	api.changeMutex.Lock()
	defer api.changeMutex.Unlock()

	// Extract or generate request ID for tracing
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	log := commitmentApiLog.WithValues("requestID", requestID, "endpoint", "/v1/change-commitments")

	// Only accept POST method
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body
	var req liquid.CommitmentChangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Error(err, "invalid request body")
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	log.Info("received change commitments request", "affectedProjects", len(req.ByProject), "dryRun", req.DryRun, "availabilityZone", req.AZ)

	// Initialize response
	resp := liquid.CommitmentChangeResponse{}

	// Check for dry run -> early reject, not supported yet
	if req.DryRun {
		resp.RejectionReason = "Dry run not supported yet"
		log.Info("rejecting dry run request")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			return
		}
		return
	}

	// Process commitment changes
	// For now, we'll implement a simplified path that checks capacity for immediate start CRs
	if err := api.processCommitmentChanges(w, log, req, &resp); err != nil {
		// Error already written to response by processCommitmentChanges
		return
	}

	// Return response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		return
	}
}

func (api *HTTPAPI) processCommitmentChanges(w http.ResponseWriter, log logr.Logger, req liquid.CommitmentChangeRequest, resp *liquid.CommitmentChangeResponse) error {
	ctx := context.Background()
	manager := NewReservationManager(api.client)
	requireRollback := false
	log.Info("processing commitment change request", "availabilityZone", req.AZ, "dryRun", req.DryRun, "affectedProjects", len(req.ByProject))

	knowledge := &reservations.FlavorGroupKnowledgeClient{Client: api.client}
	flavorGroups, err := knowledge.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		log.Info("failed to get flavor groups from knowledge extractor", "error", err)
		resp.RejectionReason = "caches not ready"
		retryTime := time.Now().Add(1 * time.Minute)
		resp.RetryAt = Some(retryTime)
		return nil
	}

	// Validate InfoVersion from request matches current version (= last content change of flavor group knowledge)
	var currentVersion int64 = -1
	if knowledgeCRD, err := knowledge.Get(ctx); err == nil && knowledgeCRD != nil && !knowledgeCRD.Status.LastContentChange.IsZero() {
		currentVersion = knowledgeCRD.Status.LastContentChange.Unix()
	}

	if req.InfoVersion != currentVersion {
		log.Info("version mismatch in commitment change request",
			"requestVersion", req.InfoVersion,
			"currentVersion", currentVersion)
		http.Error(w, fmt.Sprintf("Version mismatch: request version %d, current version %d. Please refresh and retry.",
			req.InfoVersion, currentVersion), http.StatusConflict)
		return errors.New("version mismatch")
	}

	statesBefore := make(map[string]*CommitmentState) // map of commitmentID to existing state for rollback
	var reservationsToWatch []v1alpha1.Reservation

	if req.DryRun {
		resp.RejectionReason = "Dry run not supported yet"
		return nil
	}

ProcessLoop:
	for projectID, projectChanges := range req.ByProject {
		for resourceName, resourceChanges := range projectChanges.ByResource {
			// Validate resource name pattern (instances_group_*)
			flavorGroupName, err := getFlavorGroupNameFromResource(string(resourceName))
			if err != nil {
				resp.RejectionReason = fmt.Sprintf("project with unknown resource name %s: %v", projectID, err)
				requireRollback = true
				break ProcessLoop
			}

			// Verify flavor group exists in Knowledge CRDs
			flavorGroup, flavorGroupExists := flavorGroups[flavorGroupName]
			if !flavorGroupExists {
				resp.RejectionReason = "flavor group not found: " + flavorGroupName
				requireRollback = true
				break ProcessLoop
			}

			for _, commitment := range resourceChanges.Commitments {
				// Additional per-commitment validation if needed
				log.Info("processing commitment change", "commitmentUUID", commitment.UUID, "projectID", projectID, "resourceName", resourceName, "oldStatus", commitment.OldStatus.UnwrapOr("none"), "newStatus", commitment.NewStatus.UnwrapOr("none"))

				// TODO add domain

				// List all committed resource reservations, then filter by name prefix
				var all_reservations v1alpha1.ReservationList
				if err := api.client.List(ctx, &all_reservations, client.MatchingLabels{
					v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
				}); err != nil {
					resp.RejectionReason = fmt.Sprintf("failed to list reservations for commitment %s: %v", commitment.UUID, err)
					requireRollback = true
					break ProcessLoop
				}

				// Filter by name prefix to find reservations for this commitment
				namePrefix := fmt.Sprintf("commitment-%s-", string(commitment.UUID))
				var existing_reservations v1alpha1.ReservationList
				for _, res := range all_reservations.Items {
					if len(res.Name) >= len(namePrefix) && res.Name[:len(namePrefix)] == namePrefix {
						existing_reservations.Items = append(existing_reservations.Items, res)
					}
				}

				var stateBefore *CommitmentState
				if len(existing_reservations.Items) == 0 {
					stateBefore = &CommitmentState{
						CommitmentUUID:   string(commitment.UUID),
						ProjectID:        string(projectID),
						FlavorGroupName:  flavorGroupName,
						TotalMemoryBytes: 0,
					}
				} else {
					stateBefore, err = FromReservations(existing_reservations.Items)
					if err != nil {
						resp.RejectionReason = fmt.Sprintf("failed to get existing state for commitment %s: %v", commitment.UUID, err)
						requireRollback = true
						break ProcessLoop
					}
				}
				statesBefore[string(commitment.UUID)] = stateBefore

				// get desired state
				stateDesired, err := FromChangeCommitmentTargetState(commitment, string(projectID), flavorGroupName, flavorGroup, string(req.AZ))
				if err != nil {
					resp.RejectionReason = fmt.Sprintf("failed to get desired state for commitment %s: %v", commitment.UUID, err)
					requireRollback = true
					break ProcessLoop
				}

				log.Info("applying commitment state change", "commitmentUUID", commitment.UUID, "oldState", stateBefore, "desiredState", stateDesired)

				touchedReservations, deletedReservations, err := manager.ApplyCommitmentState(ctx, log, stateDesired, flavorGroups, "changeCommitmentsApi")
				if err != nil {
					resp.RejectionReason = fmt.Sprintf("failed to apply commitment state for commitment %s: %v", commitment.UUID, err)
					requireRollback = true
					break ProcessLoop
				}
				log.Info("applied commitment state change", "commitmentUUID", commitment.UUID, "touchedReservations", len(touchedReservations), "deletedReservations", len(deletedReservations))
				reservationsToWatch = append(reservationsToWatch, touchedReservations...)
			}
		}
	}

	// TODO make the rollback defer safe
	if !requireRollback {
		log.Info("applied commitment changes, now watching for reservation readiness", "reservationsToWatch", len(reservationsToWatch))

		time_start := time.Now()

		if err := watchReservationsUntilReady(ctx, log, api.client, reservationsToWatch, watchTimeout); err != nil {
			log.Info("reservations failed to become ready, initiating rollback",
				"reason", err.Error())
			resp.RejectionReason = fmt.Sprintf("Not all reservations can be fulfilled: %v", err)
			requireRollback = true
		}

		log.Info("finished watching reservation", "totalSchedulingTimeSeconds", time.Since(time_start).Seconds())
	}

	if requireRollback {
		log.Info("rollback of commitment changes")
		for commitmentUUID, state := range statesBefore {
			// Rollback to statesBefore for this commitment
			log.Info("applying rollback for commitment", "commitmentUUID", commitmentUUID, "stateBefore", state)
			_, _, err := manager.ApplyCommitmentState(ctx, log, state, flavorGroups, "changeCommitmentsApiRollback")
			if err != nil {
				log.Info("failed to apply rollback state for commitment", "commitmentUUID", commitmentUUID, "error", err)
				// continue with best effort rollback for other projects
			}
		}

		log.Info("finished applying rollbacks for commitment changes", "reasonOfRollback", resp.RejectionReason)

		// TODO improve human-readable reasoning based on actual failure, i.e. polish resp.RejectionReason
		return nil
	}

	log.Info("commitment changes accepted")
	if resp.RejectionReason != "" {
		log.Info("unexpected non-empty rejection reason without rollback", "reason", resp.RejectionReason)
		resp.RejectionReason = ""
	}
	return nil
}

// watchReservationsUntilReady polls until all reservations reach Ready=True or timeout.
func watchReservationsUntilReady(
	ctx context.Context,
	log logr.Logger,
	k8sClient client.Client,
	reservations []v1alpha1.Reservation,
	timeout time.Duration,
) error {

	if len(reservations) == 0 {
		return nil
	}

	deadline := time.Now().Add(timeout)

	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout after %v waiting for reservations to become ready", timeout)
		}

		allReady := true
		var notReadyReasons []string

		for _, res := range reservations {
			// Fetch current state
			var current v1alpha1.Reservation
			nn := types.NamespacedName{
				Name:      res.Name,
				Namespace: res.Namespace,
			}

			if err := k8sClient.Get(ctx, nn, &current); err != nil {
				if apierrors.IsNotFound(err) {
					// Reservation is still in process of being created
					allReady = false
					continue
				}
				return fmt.Errorf("failed to get reservation %s: %w", res.Name, err)
			}

			// Check Ready condition
			readyCond := meta.FindStatusCondition(
				current.Status.Conditions,
				v1alpha1.ReservationConditionReady,
			)

			if readyCond == nil {
				// Condition not set yet, keep waiting
				allReady = false
				notReadyReasons = append(notReadyReasons,
					res.Name+": condition not set")
				continue
			}

			switch readyCond.Status {
			case metav1.ConditionTrue:
				// This reservation is ready
				continue
			case metav1.ConditionFalse:
				// Explicit failure - stop immediately
				return fmt.Errorf("reservation %s failed: %s (reason: %s)",
					res.Name, readyCond.Message, readyCond.Reason)
			case metav1.ConditionUnknown:
				// Still processing
				allReady = false
				notReadyReasons = append(notReadyReasons,
					fmt.Sprintf("%s: %s", res.Name, readyCond.Message))
			}
		}

		if allReady {
			log.Info("all reservations are ready",
				"count", len(reservations))
			return nil
		}

		// Log progress
		log.Info("waiting for reservations to become ready",
			"notReady", len(notReadyReasons),
			"total", len(reservations),
			"timeRemaining", time.Until(deadline).Round(time.Second))

		// Wait before next poll
		select {
		case <-time.After(pollInterval):
			// Continue polling
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for reservations: %w", ctx.Err())
		}
	}
}
