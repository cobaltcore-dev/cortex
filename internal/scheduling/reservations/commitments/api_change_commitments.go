// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"github.com/sapcc/go-api-declarations/liquid"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// sortedKeys returns map keys sorted alphabetically for deterministic iteration.
func sortedKeys[K ~string, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return string(keys[i]) < string(keys[j])
	})
	return keys
}

// implements POST /commitments/v1/change-commitments from Limes LIQUID API:
// See: https://github.com/sapcc/go-api-declarations/blob/main/liquid/commitment.go
// See: https://pkg.go.dev/github.com/sapcc/go-api-declarations/liquid
//
// This endpoint handles commitment changes by creating/updating/deleting Reservation CRDs based on the commitment lifecycle.
// A request may contain multiple commitment changes which are processed in a single transaction. If any change fails, all changes are rolled back.
func (api *HTTPAPI) HandleChangeCommitments(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	// Initialize
	resp := liquid.CommitmentChangeResponse{}
	req := liquid.CommitmentChangeRequest{}
	statusCode := http.StatusOK

	// Extract or generate request ID for tracing - always set in response header
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = uuid.New().String()
	}
	w.Header().Set("X-Request-ID", requestID)

	// Check if API is enabled
	if !api.config.EnableChangeCommitmentsAPI {
		statusCode = http.StatusServiceUnavailable
		http.Error(w, "change-commitments API is disabled", statusCode)
		api.recordMetrics(req, resp, statusCode, startTime)
		return
	}

	// Serialize all change-commitments requests (shared with syncer via distributed lock)
	ctx := reservations.WithGlobalRequestID(context.Background(), "committed-resource-"+requestID)
	_, unlock, err := api.crMutex.Lock(ctx)
	if err != nil {
		logger := LoggerFromContext(ctx).WithValues("component", "api", "endpoint", "/commitments/v1/change-commitments")
		logger.Error(err, "failed to acquire distributed lock for change-commitments")
		statusCode = http.StatusServiceUnavailable
		http.Error(w, "Failed to acquire lock, please retry later: "+err.Error(), statusCode)
		api.recordMetrics(req, resp, statusCode, startTime)
		return
	}
	defer unlock()

	logger := LoggerFromContext(ctx).WithValues("component", "api", "endpoint", "/commitments/v1/change-commitments")

	// Only accept POST method
	if r.Method != http.MethodPost {
		statusCode = http.StatusMethodNotAllowed
		http.Error(w, "Method not allowed", statusCode)
		api.recordMetrics(req, resp, statusCode, startTime)
		return
	}

	// Parse request body
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Error(err, "invalid request body")
		statusCode = http.StatusBadRequest
		http.Error(w, "Invalid request body: "+err.Error(), statusCode)
		api.recordMetrics(req, resp, statusCode, startTime)
		return
	}

	logger.Info("received change commitments request", "affectedProjects", len(req.ByProject), "dryRun", req.DryRun, "availabilityZone", req.AZ)

	// Check for dry run -> early reject, not supported yet
	if req.DryRun {
		resp.RejectionReason = "Dry run not supported yet"
		api.recordMetrics(req, resp, statusCode, startTime)
		logger.Info("rejecting dry run request")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			return
		}
		return
	}

	// Process commitment changes
	// For now, we'll implement a simplified path that checks capacity for immediate start CRs

	if err := api.processCommitmentChanges(ctx, w, logger, req, &resp); err != nil {
		// Error already written to response by processCommitmentChanges
		// Determine status code from error context (409 or 503)
		if strings.Contains(err.Error(), "version mismatch") {
			statusCode = http.StatusConflict
		} else if strings.Contains(err.Error(), "caches not ready") {
			statusCode = http.StatusServiceUnavailable
		}
		// Record metrics for error cases
		api.recordMetrics(req, resp, statusCode, startTime)
		return
	}

	// Record metrics
	api.recordMetrics(req, resp, statusCode, startTime)

	// Return response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		return
	}
}

func (api *HTTPAPI) processCommitmentChanges(ctx context.Context, w http.ResponseWriter, logger logr.Logger, req liquid.CommitmentChangeRequest, resp *liquid.CommitmentChangeResponse) error {
	manager := NewReservationManager(api.client)
	requireRollback := false
	failedCommitments := make(map[string]string) // commitmentUUID to reason for failure, for better response messages in case of rollback
	creatorRequestID := reservations.GlobalRequestIDFromContext(ctx)

	knowledge := &reservations.FlavorGroupKnowledgeClient{Client: api.client}
	flavorGroups, err := knowledge.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		logger.Info("failed to get flavor groups from knowledge extractor", "error", err)
		http.Error(w, "caches not ready, please retry later", http.StatusServiceUnavailable)
		return errors.New("caches not ready")
	}

	// Validate InfoVersion from request matches current version (= last content change of flavor group knowledge)
	var currentVersion int64 = -1
	if knowledgeCRD, err := knowledge.Get(ctx); err == nil && knowledgeCRD != nil && !knowledgeCRD.Status.LastContentChange.IsZero() {
		currentVersion = knowledgeCRD.Status.LastContentChange.Unix()
	}

	if req.InfoVersion != currentVersion {
		logger.Info("version mismatch in commitment change request",
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
	for _, projectID := range sortedKeys(req.ByProject) {
		projectChanges := req.ByProject[projectID]
		for _, resourceName := range sortedKeys(projectChanges.ByResource) {
			resourceChanges := projectChanges.ByResource[resourceName]
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

			// Reject commitments for flavor groups that don't accept CRs
			if !FlavorGroupAcceptsCommitments(&flavorGroup) {
				resp.RejectionReason = FlavorGroupCommitmentRejectionReason(&flavorGroup)
				requireRollback = true
				break ProcessLoop
			}

			for _, commitment := range resourceChanges.Commitments {
				logger.V(1).Info("processing commitment", "commitmentUUID", commitment.UUID, "oldStatus", commitment.OldStatus.UnwrapOr("none"), "newStatus", commitment.NewStatus.UnwrapOr("none"))

				// TODO add configurable upper limit validation for commitment size (number of instances) to prevent excessive reservation creation
				// TODO add domain

				// List all committed resource reservations, then filter by name prefix
				var all_reservations v1alpha1.ReservationList
				if err := api.client.List(ctx, &all_reservations, client.MatchingLabels{
					v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
				}); err != nil {
					failedCommitments[string(commitment.UUID)] = "failed to list reservations"
					logger.Info("failed to list reservations for commitment", "commitmentUUID", commitment.UUID, "error", err)
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
						failedCommitments[string(commitment.UUID)] = "failed to parse existing commitment reservations"
						logger.Info("failed to get existing state for commitment", "commitmentUUID", commitment.UUID, "error", err)
						requireRollback = true
						break ProcessLoop
					}
				}
				statesBefore[string(commitment.UUID)] = stateBefore

				// get desired state
				stateDesired, err := FromChangeCommitmentTargetState(commitment, string(projectID), flavorGroupName, flavorGroup, string(req.AZ))
				if err != nil {
					failedCommitments[string(commitment.UUID)] = err.Error()
					logger.Info("failed to get desired state for commitment", "commitmentUUID", commitment.UUID, "error", err)
					requireRollback = true
					break ProcessLoop
				}
				// Set creator request ID for traceability across controller reconciles
				stateDesired.CreatorRequestID = creatorRequestID

				logger.V(1).Info("applying commitment state change", "commitmentUUID", commitment.UUID, "oldMemory", stateBefore.TotalMemoryBytes, "desiredMemory", stateDesired.TotalMemoryBytes)

				applyResult, err := manager.ApplyCommitmentState(ctx, logger, stateDesired, flavorGroups, "changeCommitmentsApi")
				if err != nil {
					failedCommitments[string(commitment.UUID)] = "failed to apply commitment state"
					logger.Info("failed to apply commitment state for commitment", "commitmentUUID", commitment.UUID, "error", err)
					requireRollback = true
					break ProcessLoop
				}
				logger.V(1).Info("applied commitment state change", "commitmentUUID", commitment.UUID, "touchedReservations", len(applyResult.TouchedReservations), "deletedReservations", len(applyResult.RemovedReservations))
				reservationsToWatch = append(reservationsToWatch, applyResult.TouchedReservations...)
			}
		}
	}

	// TODO make the rollback defer safe
	if !requireRollback {
		logger.Info("applied commitment changes, now watching for reservation readiness", "reservationsToWatch", len(reservationsToWatch))

		time_start := time.Now()

		if failedReservations, errors := watchReservationsUntilReady(ctx, logger, api.client, reservationsToWatch, api.config.ChangeAPIWatchReservationsTimeout, api.config.ChangeAPIWatchReservationsPollInterval); len(failedReservations) > 0 || len(errors) > 0 {
			logger.Info("reservations failed to become ready, initiating rollback",
				"failedReservations", len(failedReservations),
				"errors", errors)

			for _, res := range failedReservations {
				failedCommitments[res.Spec.CommittedResourceReservation.CommitmentUUID] = "not sufficient capacity"
			}
			if len(failedReservations) == 0 {
				resp.RejectionReason += "timeout reached while processing commitment changes"
				api.monitor.timeouts.Inc()
			}
			requireRollback = true
		}

		logger.Info("finished watching reservation", "totalSchedulingTimeSeconds", time.Since(time_start).Seconds())
	}

	if requireRollback {
		// Build rejection reason from failed commitments
		if len(failedCommitments) > 0 {
			var reasonBuilder strings.Builder
			reasonBuilder.WriteString(fmt.Sprintf("%d commitment(s) failed to apply: ", len(failedCommitments)))
			for commitmentUUID, reason := range failedCommitments {
				reasonBuilder.WriteString(fmt.Sprintf("\n- commitment %s: %s", commitmentUUID, reason))
			}
			resp.RejectionReason = reasonBuilder.String()
		}

		logger.Info("rollback of commitment changes")
		for commitmentUUID, state := range statesBefore {
			// Rollback to statesBefore for this commitment
			logger.Info("applying rollback for commitment", "commitmentUUID", commitmentUUID, "stateBefore", state)
			_, err := manager.ApplyCommitmentState(ctx, logger, state, flavorGroups, "changeCommitmentsApiRollback")
			if err != nil {
				logger.Info("failed to apply rollback state for commitment", "commitmentUUID", commitmentUUID, "error", err)
				// continue with best effort rollback for other projects
			}
		}

		logger.Info("finished applying rollbacks for commitment changes", "reasonOfRollback", resp.RejectionReason)
		return nil
	}

	logger.Info("commitment changes accepted")
	return nil
}

// watchReservationsUntilReady polls until all reservations reach Ready=True or timeout.
// Returns failed reservations and any errors encountered.
func watchReservationsUntilReady(
	ctx context.Context,
	logger logr.Logger,
	k8sClient client.Client,
	reservations []v1alpha1.Reservation,
	timeout time.Duration,
	pollInterval time.Duration,
) (failedReservations []v1alpha1.Reservation, errors []error) {

	if len(reservations) == 0 {
		return failedReservations, nil
	}

	deadline := time.Now().Add(timeout)
	startTime := time.Now()
	totalReservations := len(reservations)

	reservationsToWatch := make([]v1alpha1.Reservation, len(reservations))
	copy(reservationsToWatch, reservations)

	// Track successful reservations for summary
	var successfulReservations []string
	pollCount := 0

	for {
		pollCount++
		var stillWaiting []v1alpha1.Reservation
		if time.Now().After(deadline) {
			errors = append(errors, fmt.Errorf("timeout after %v waiting for reservations to become ready", timeout))
			// Log summary on timeout
			logger.Info("reservation watch completed (timeout)",
				"total", totalReservations,
				"ready", len(successfulReservations),
				"failed", len(failedReservations),
				"timedOut", len(reservationsToWatch),
				"duration", time.Since(startTime).Round(time.Millisecond),
				"polls", pollCount)
			return failedReservations, errors
		}

		for _, res := range reservationsToWatch {
			// Fetch current state
			var current v1alpha1.Reservation
			nn := types.NamespacedName{
				Name:      res.Name,
				Namespace: res.Namespace,
			}

			if err := k8sClient.Get(ctx, nn, &current); err != nil {
				// Reservation is still in process of being created, or there is a transient error
				stillWaiting = append(stillWaiting, res)
				continue
			}

			// Check Ready condition
			readyCond := meta.FindStatusCondition(
				current.Status.Conditions,
				v1alpha1.ReservationConditionReady,
			)

			if readyCond == nil {
				// Condition not set yet, keep waiting
				stillWaiting = append(stillWaiting, res)
				continue
			}

			switch readyCond.Status {
			case metav1.ConditionTrue:
				// Only consider truly ready if Status.Host is populated
				if current.Spec.TargetHost == "" || current.Status.Host == "" {
					stillWaiting = append(stillWaiting, res)
					continue
				}
				// Reservation is successfully scheduled - track for summary
				successfulReservations = append(successfulReservations, current.Name)

			case metav1.ConditionFalse:
				// Any failure reason counts as failed
				failedReservations = append(failedReservations, current)
			case metav1.ConditionUnknown:
				stillWaiting = append(stillWaiting, res)
			}
		}

		if len(stillWaiting) == 0 {
			// All reservations have reached a terminal state - log summary
			logger.Info("reservation watch completed",
				"total", totalReservations,
				"ready", len(successfulReservations),
				"failed", len(failedReservations),
				"duration", time.Since(startTime).Round(time.Millisecond),
				"polls", pollCount)
			return failedReservations, errors
		}

		reservationsToWatch = stillWaiting

		// Wait before next poll
		select {
		case <-time.After(pollInterval):
			// Continue polling
		case <-ctx.Done():
			return failedReservations, append(errors, fmt.Errorf("context cancelled while waiting for reservations: %w", ctx.Err()))
		}
	}
}
