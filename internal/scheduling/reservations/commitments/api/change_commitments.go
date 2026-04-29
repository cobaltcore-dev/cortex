// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

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
	commitments "github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/commitments"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"github.com/sapcc/go-api-declarations/liquid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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

// crSnapshot captures a CommittedResource CRD's prior state for batch rollback.
// prevSpec is nil when the CRD was newly created (i.e. did not exist before the batch).
type crSnapshot struct {
	crName   string
	prevSpec *v1alpha1.CommittedResourceSpec
}

// HandleChangeCommitments implements POST /commitments/v1/change-commitments from the Limes LIQUID API.
// It writes CommittedResource CRDs (one per commitment) and polls their status conditions until
// the controller confirms or rejects each one. On any failure the whole batch is rolled back.
func (api *HTTPAPI) HandleChangeCommitments(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	resp := liquid.CommitmentChangeResponse{}
	req := liquid.CommitmentChangeRequest{}
	statusCode := http.StatusOK

	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = uuid.New().String()
	}
	w.Header().Set("X-Request-ID", requestID)

	if !api.config.EnableChangeCommitmentsAPI {
		statusCode = http.StatusServiceUnavailable
		http.Error(w, "change-commitments API is disabled", statusCode)
		api.recordMetrics(req, resp, statusCode, startTime)
		return
	}

	// Serialize all change-commitments requests so the controller sees a consistent world.
	api.changeMutex.Lock()
	defer api.changeMutex.Unlock()

	ctx := reservations.WithGlobalRequestID(context.Background(), "committed-resource-"+requestID)
	logger := commitments.LoggerFromContext(ctx).WithValues("component", "api", "endpoint", "/commitments/v1/change-commitments")

	if r.Method != http.MethodPost {
		statusCode = http.StatusMethodNotAllowed
		http.Error(w, "Method not allowed", statusCode)
		api.recordMetrics(req, resp, statusCode, startTime)
		return
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Error(err, "invalid request body")
		statusCode = http.StatusBadRequest
		http.Error(w, "Invalid request body: "+err.Error(), statusCode)
		api.recordMetrics(req, resp, statusCode, startTime)
		return
	}

	logger.Info("received change commitments request", "affectedProjects", len(req.ByProject), "dryRun", req.DryRun, "availabilityZone", req.AZ)

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

	if err := api.processCommitmentChanges(ctx, w, logger, req, &resp); err != nil {
		if strings.Contains(err.Error(), "version mismatch") {
			statusCode = http.StatusConflict
		} else if strings.Contains(err.Error(), "caches not ready") {
			statusCode = http.StatusServiceUnavailable
		}
		api.recordMetrics(req, resp, statusCode, startTime)
		return
	}

	api.recordMetrics(req, resp, statusCode, startTime)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		return
	}
}

func (api *HTTPAPI) processCommitmentChanges(ctx context.Context, w http.ResponseWriter, logger logr.Logger, req liquid.CommitmentChangeRequest, resp *liquid.CommitmentChangeResponse) error {
	knowledge := &reservations.FlavorGroupKnowledgeClient{Client: api.client}
	flavorGroups, err := knowledge.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		logger.Info("failed to get flavor groups from knowledge extractor", "error", err)
		http.Error(w, "caches not ready, please retry later", http.StatusServiceUnavailable)
		return errors.New("caches not ready")
	}

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

	var (
		toWatch      []string     // CRD names to poll for terminal conditions
		snapshots    []crSnapshot // ordered list for deterministic rollback
		failedReason string
		rollback     bool
	)

ProcessLoop:
	for _, projectID := range sortedKeys(req.ByProject) {
		projectChanges := req.ByProject[projectID]
		for _, resourceName := range sortedKeys(projectChanges.ByResource) {
			resourceChanges := projectChanges.ByResource[resourceName]

			flavorGroupName, err := commitments.GetFlavorGroupNameFromResource(string(resourceName))
			if err != nil {
				failedReason = fmt.Sprintf("project with unknown resource name %s: %v", projectID, err)
				rollback = true
				break ProcessLoop
			}

			flavorGroup, ok := flavorGroups[flavorGroupName]
			if !ok {
				failedReason = "flavor group not found: " + flavorGroupName
				rollback = true
				break ProcessLoop
			}

			if !commitments.FlavorGroupAcceptsCommitments(&flavorGroup) {
				failedReason = commitments.FlavorGroupCommitmentRejectionReason(&flavorGroup)
				rollback = true
				break ProcessLoop
			}

			for _, commitment := range resourceChanges.Commitments {
				logger.V(1).Info("processing commitment",
					"commitmentUUID", commitment.UUID,
					"oldStatus", commitment.OldStatus.UnwrapOr("none"),
					"newStatus", commitment.NewStatus.UnwrapOr("none"))

				stateDesired, err := commitments.FromChangeCommitmentTargetState(
					commitment, string(projectID), flavorGroupName, flavorGroup, string(req.AZ))
				if err != nil {
					failedReason = fmt.Sprintf("commitment %s: %s", commitment.UUID, err)
					rollback = true
					break ProcessLoop
				}

				crName := "commitment-" + string(commitment.UUID)

				// Snapshot the current spec before mutation so we can restore it on rollback.
				snap := crSnapshot{crName: crName}
				existing := &v1alpha1.CommittedResource{}
				if err := api.client.Get(ctx, types.NamespacedName{Name: crName}, existing); err != nil {
					if !apierrors.IsNotFound(err) {
						failedReason = fmt.Sprintf("commitment %s: failed to read pre-update snapshot: %v", commitment.UUID, err)
						rollback = true
						break ProcessLoop
					}
					// Not found: CR is new, prevSpec stays nil.
				} else {
					specCopy := existing.Spec
					snap.prevSpec = &specCopy
				}

				// Upsert CommittedResource CRD. AllowRejection=true: the controller may reject
				// and roll back child Reservations if placement fails — the API needs a final answer.
				cr := &v1alpha1.CommittedResource{}
				cr.Name = crName
				if _, err := controllerutil.CreateOrUpdate(ctx, api.client, cr, func() error {
					applyCRSpec(cr, stateDesired, true)
					return nil
				}); err != nil {
					failedReason = fmt.Sprintf("commitment %s: failed to write CommittedResource CRD: %v", commitment.UUID, err)
					rollback = true
					break ProcessLoop
				}

				toWatch = append(toWatch, crName)
				snapshots = append(snapshots, snap)
				logger.V(1).Info("upserted CommittedResource CRD", "name", crName)
			}
		}
	}

	if !rollback {
		logger.Info("CommittedResource CRDs written, polling for controller outcome", "count", len(toWatch))
		watchStart := time.Now()

		rejected, watchErrs := watchCRsUntilReady(
			ctx, logger, api.client, toWatch,
			api.config.ChangeAPIWatchReservationsTimeout,
			api.config.ChangeAPIWatchReservationsPollInterval,
		)

		logger.Info("polling complete", "duration", time.Since(watchStart).Round(time.Millisecond))

		switch {
		case len(rejected) > 0:
			var b strings.Builder
			fmt.Fprintf(&b, "%d commitment(s) failed to apply:", len(rejected))
			for _, crName := range toWatch { // iterate toWatch for deterministic order
				if reason, ok := rejected[crName]; ok {
					fmt.Fprintf(&b, "\n- commitment %s: %s", strings.TrimPrefix(crName, "commitment-"), reason)
				}
			}
			failedReason = b.String()
			rollback = true
		case len(watchErrs) > 0:
			msgs := make([]string, len(watchErrs))
			for i, e := range watchErrs {
				msgs[i] = e.Error()
			}
			failedReason = "timeout reached while processing commitment changes: " + strings.Join(msgs, "; ")
			api.monitor.timeouts.Inc()
			rollback = true
		}
	}

	if rollback {
		resp.RejectionReason = failedReason
		logger.Info("rolling back CommittedResource CRDs", "reason", failedReason, "count", len(snapshots))
		for i := len(snapshots) - 1; i >= 0; i-- {
			rollbackCR(ctx, logger, api.client, snapshots[i])
		}
		logger.Info("rollback complete")
		return nil
	}

	logger.Info("commitment changes accepted")
	return nil
}

// watchCRsUntilReady polls CommittedResource conditions until each CRD reaches a terminal state:
//   - Ready=True (Accepted) — success
//   - Ready=False, Reason=Planned — success; controller reserves capacity at activation time
//   - Ready=False, Reason=Rejected — failure; reason reported to caller
//
// Returns a map of crName → rejection reason for failed CRDs, and any polling errors (e.g. timeout).
func watchCRsUntilReady(
	ctx context.Context,
	logger logr.Logger,
	k8sClient client.Client,
	crNames []string,
	timeout time.Duration,
	pollInterval time.Duration,
) (rejected map[string]string, errs []error) {

	if len(crNames) == 0 {
		return nil, nil
	}

	rejected = make(map[string]string)
	deadline := time.Now().Add(timeout)

	pending := make(map[string]struct{}, len(crNames))
	for _, name := range crNames {
		pending[name] = struct{}{}
	}

	for {
		if time.Now().After(deadline) {
			errs = append(errs, fmt.Errorf("timeout after %v waiting for %d CommittedResource CRD(s)", timeout, len(pending)))
			return rejected, errs
		}

		for name := range pending {
			cr := &v1alpha1.CommittedResource{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, cr); err != nil {
				continue // transient; keep waiting
			}

			cond := meta.FindStatusCondition(cr.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
			if cond == nil {
				continue // controller hasn't reconciled yet
			}

			switch {
			case cond.Status == metav1.ConditionTrue:
				delete(pending, name)
			case cond.Status == metav1.ConditionFalse && cond.Reason == v1alpha1.CommittedResourceReasonPlanned:
				delete(pending, name) // planned = accepted; controller will reserve at activation
			case cond.Status == metav1.ConditionFalse && cond.Reason == v1alpha1.CommittedResourceReasonRejected:
				delete(pending, name)
				rejected[name] = cond.Message
				// Reason=Reserving: controller is placing slots; keep waiting.
			}
		}

		if len(pending) == 0 {
			return rejected, nil
		}

		select {
		case <-time.After(pollInterval):
		case <-ctx.Done():
			return rejected, append(errs, fmt.Errorf("context cancelled: %w", ctx.Err()))
		}
		logger.V(1).Info("polling CommittedResource CRDs", "pending", len(pending))
	}
}

// rollbackCR reverses the batch-local change to a single CommittedResource CRD.
// If the CRD was newly created (snap.prevSpec == nil) it is deleted.
// If it was updated, its spec is restored to the snapshot.
func rollbackCR(ctx context.Context, logger logr.Logger, k8sClient client.Client, snap crSnapshot) {
	if snap.prevSpec == nil {
		cr := &v1alpha1.CommittedResource{}
		cr.Name = snap.crName
		if err := k8sClient.Delete(ctx, cr); client.IgnoreNotFound(err) != nil {
			logger.Error(err, "failed to delete CommittedResource CRD during rollback", "name", snap.crName)
		}
		return
	}

	cr := &v1alpha1.CommittedResource{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: snap.crName}, cr); err != nil {
		logger.Error(err, "failed to fetch CommittedResource CRD for rollback", "name", snap.crName)
		return
	}
	cr.Spec = *snap.prevSpec
	if err := k8sClient.Update(ctx, cr); err != nil {
		logger.Error(err, "failed to restore CommittedResource CRD spec during rollback", "name", snap.crName)
	}
}

// applyCRSpec writes CommitmentState fields into a CommittedResource CRD spec.
// allowRejection=true for the change-commitments API path: the controller may reject
// on failure and the API reports the outcome to Limes.
func applyCRSpec(cr *v1alpha1.CommittedResource, state *commitments.CommitmentState, allowRejection bool) {
	cr.Spec.CommitmentUUID = state.CommitmentUUID
	cr.Spec.SchedulingDomain = v1alpha1.SchedulingDomainNova
	cr.Spec.FlavorGroupName = state.FlavorGroupName
	cr.Spec.ResourceType = v1alpha1.CommittedResourceTypeMemory
	cr.Spec.Amount = *resource.NewQuantity(state.TotalMemoryBytes, resource.BinarySI)
	cr.Spec.AvailabilityZone = state.AvailabilityZone
	cr.Spec.ProjectID = state.ProjectID
	cr.Spec.DomainID = state.DomainID
	cr.Spec.State = state.State
	cr.Spec.AllowRejection = allowRejection

	if state.StartTime != nil {
		t := metav1.NewTime(*state.StartTime)
		cr.Spec.StartTime = &t
	} else {
		cr.Spec.StartTime = nil
	}
	if state.EndTime != nil {
		t := metav1.NewTime(*state.EndTime)
		cr.Spec.EndTime = &t
	} else {
		cr.Spec.EndTime = nil
	}
}
