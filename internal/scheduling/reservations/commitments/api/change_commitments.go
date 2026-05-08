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
	"k8s.io/client-go/util/retry"
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

// crWatch pairs a CRD name with the generation written by the API so the polling loop
// can skip cache reads that have not yet reflected the write (stale-cache guard).
type crWatch struct {
	name       string
	generation int64
}

// crSnapshot captures a CommittedResource CRD's prior state for batch rollback.
// prevSpec is nil when the CRD was newly created (i.e. did not exist before the batch).
// wasDeleted is true when the batch operation deleted the CRD; rollback must re-create it.
type crSnapshot struct {
	crName     string
	prevSpec   *v1alpha1.CommittedResourceSpec
	wasDeleted bool
}

// HandleChangeCommitments implements POST /commitments/v1/change-commitments from the Limes LIQUID API.
// It writes CommittedResource CRDs (one per commitment) and polls their status conditions until
// the controller confirms or rejects each one. On any failure the whole batch is rolled back.
//
// See: https://github.com/sapcc/go-api-declarations/blob/main/liquid/commitment.go
// See: https://pkg.go.dev/github.com/sapcc/go-api-declarations/liquid
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

	if !api.config.EnableChangeCommitments {
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

	// If Limes does not require confirmation for this batch (e.g. deletions, status-only transitions),
	// the controller must not reject — it must retry until it succeeds (AllowRejection=false).
	// Conversely, when Limes requires confirmation, the controller may reject and report back.
	allowRejection := req.RequiresConfirmation()

	var (
		toWatch      []crWatch    // CRD names + expected generations to poll for terminal conditions (upserts only)
		snapshots    []crSnapshot // ordered list for deterministic rollback
		failedReason string
		rollback     bool
	)

ProcessLoop:
	for _, projectID := range sortedKeys(req.ByProject) {
		projectChanges := req.ByProject[projectID]

		// Extract domain ID from Keystone project metadata if Limes provided it.
		domainID := ""
		if pm := projectChanges.ProjectMetadata.UnwrapOr(liquid.ProjectMetadata{}); pm.Domain.UUID != "" {
			domainID = pm.Domain.UUID
		}

		for _, resourceName := range sortedKeys(projectChanges.ByResource) {
			resourceChanges := projectChanges.ByResource[resourceName]

			flavorGroupName, err := commitments.GetFlavorGroupNameFromResource(string(resourceName))
			if err != nil {
				failedReason = fmt.Sprintf("project with unknown resource name %s: %v", projectID, err)
				rollback = true
				break ProcessLoop
			}

			if _, ok := flavorGroups[flavorGroupName]; !ok {
				failedReason = "flavor group not found: " + flavorGroupName
				rollback = true
				break ProcessLoop
			}

			if !api.config.ResourceConfigForGroup(flavorGroupName).RAM.HandlesCommitments {
				failedReason = fmt.Sprintf("flavor group %q is not configured to handle commitments", flavorGroupName)
				rollback = true
				break ProcessLoop
			}

			for _, commitment := range resourceChanges.Commitments {
				isDelete := commitment.NewStatus.IsNone()
				crName := "commitment-" + string(commitment.UUID)

				logger.Info("processing commitment",
					"commitmentUUID", commitment.UUID,
					"oldStatus", commitment.OldStatus.UnwrapOr("none"),
					"newStatus", commitment.NewStatus.UnwrapOr("none"),
					"delete", isDelete)

				// Snapshot the current spec before mutation so we can restore it on rollback.
				snap := crSnapshot{crName: crName}
				existing := &v1alpha1.CommittedResource{}
				if err := api.client.Get(ctx, types.NamespacedName{Name: crName}, existing); err != nil {
					if !apierrors.IsNotFound(err) {
						failedReason = fmt.Sprintf("commitment %s: failed to read pre-update snapshot: %v", commitment.UUID, err)
						rollback = true
						break ProcessLoop
					}
					// Not found: CR is new (or already absent for deletes), prevSpec stays nil.
				} else {
					specCopy := existing.Spec
					snap.prevSpec = &specCopy
				}

				if isDelete {
					// Limes is removing this commitment; delete the CRD if it exists.
					snap.wasDeleted = true
					snapshots = append(snapshots, snap)
					if snap.prevSpec != nil {
						if err := api.client.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
							failedReason = fmt.Sprintf("commitment %s: failed to delete CommittedResource CRD: %v", commitment.UUID, err)
							rollback = true
							break ProcessLoop
						}
						if err := commitments.DeleteChildReservations(ctx, api.client, existing); err != nil {
							failedReason = fmt.Sprintf("commitment %s: failed to delete child reservations: %v", commitment.UUID, err)
							rollback = true
							break ProcessLoop
						}
						logger.V(1).Info("deleted CommittedResource CRD", "name", crName)
					}
					continue
				}

				stateDesired, err := commitments.FromChangeCommitmentTargetState(
					commitment, string(projectID), domainID, flavorGroupName, string(req.AZ))
				if err != nil {
					failedReason = fmt.Sprintf("commitment %s: %s", commitment.UUID, err)
					rollback = true
					break ProcessLoop
				}

				cr := &v1alpha1.CommittedResource{}
				cr.Name = crName
				if _, err := controllerutil.CreateOrUpdate(ctx, api.client, cr, func() error {
					if cr.Spec.AvailabilityZone != "" && cr.Spec.AvailabilityZone != stateDesired.AvailabilityZone {
						return fmt.Errorf("cannot change availability zone of commitment %s: current=%q requested=%q",
							commitment.UUID, cr.Spec.AvailabilityZone, stateDesired.AvailabilityZone)
					}
					applyCRSpec(cr, stateDesired, allowRejection)
					if cr.Annotations == nil {
						cr.Annotations = make(map[string]string)
					}
					cr.Annotations[v1alpha1.AnnotationCreatorRequestID] = reservations.GlobalRequestIDFromContext(ctx)
					return nil
				}); err != nil {
					failedReason = fmt.Sprintf("commitment %s: failed to write CommittedResource CRD: %v", commitment.UUID, err)
					rollback = true
					break ProcessLoop
				}

				toWatch = append(toWatch, crWatch{name: crName, generation: cr.Generation})
				snapshots = append(snapshots, snap)
				logger.V(1).Info("upserted CommittedResource CRD", "name", crName)
			}
		}
	}

	if !rollback {
		// Non-confirming changes (RequiresConfirmation=false): Limes ignores our RejectionReason,
		// so there is no point blocking on the controller outcome. The CRDs are written with
		// AllowRejection=false, meaning the controller will retry indefinitely in the background.
		if !allowRejection {
			logger.Info("non-confirming changes applied, returning without polling", "count", len(toWatch))
			return nil
		}

		logger.Info("CommittedResource CRDs written, polling for controller outcome", "count", len(toWatch))
		watchStart := time.Now()

		rejected, watchErrs := watchCRsUntilReady(
			ctx, logger, api.client, toWatch,
			api.config.WatchTimeout.Duration,
			api.config.WatchPollInterval.Duration,
		)

		logger.Info("polling complete", "duration", time.Since(watchStart).Round(time.Millisecond))

		switch {
		case len(rejected) > 0:
			var b strings.Builder
			fmt.Fprintf(&b, "%d commitment(s) failed to apply:", len(rejected))
			for _, w := range toWatch { // iterate toWatch for deterministic order
				if reason, ok := rejected[w.name]; ok {
					fmt.Fprintf(&b, "\n- commitment %s: %s", strings.TrimPrefix(w.name, "commitment-"), reason)
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
// Each entry in watches carries the generation written by the API. The polling loop skips any
// cache read whose generation is older than that value, preventing a stale Ready=True (or
// Ready=False/Rejected) condition from a prior reconcile cycle from being mistaken for the
// outcome of the current write.
//
// Returns a map of crName → rejection reason for failed CRDs, and any polling errors (e.g. timeout).
func watchCRsUntilReady(
	ctx context.Context,
	logger logr.Logger,
	k8sClient client.Client,
	watches []crWatch,
	timeout time.Duration,
	pollInterval time.Duration,
) (rejected map[string]string, errs []error) {

	if len(watches) == 0 {
		return nil, nil
	}

	rejected = make(map[string]string)
	deadline := time.Now().Add(timeout)

	// pending maps CR name → the minimum generation the cache must show before we trust conditions.
	pending := make(map[string]int64, len(watches))
	for _, w := range watches {
		pending[w.name] = w.generation
	}

	for {
		if time.Now().After(deadline) {
			errs = append(errs, fmt.Errorf("timeout after %v waiting for %d CommittedResource CRD(s)", timeout, len(pending)))
			return rejected, errs
		}

		for name, expectedGen := range pending {
			cr := &v1alpha1.CommittedResource{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, cr); err != nil {
				continue // transient; keep waiting
			}

			// The informer cache may not have caught up with the spec write yet. Until the
			// cache reflects at least the generation we wrote, any condition we read belongs
			// to an older spec version and must not be treated as terminal.
			if cr.Generation < expectedGen {
				logger.V(1).Info("cache not yet reflecting write, skipping",
					"name", name,
					"cacheGeneration", cr.Generation,
					"expectedGeneration", expectedGen,
				)
				continue
			}

			cond := meta.FindStatusCondition(cr.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
			if cond == nil {
				continue // controller hasn't reconciled yet
			}
			// Skip conditions stamped by a prior reconcile: ObservedGeneration < Generation means
			// the condition reflects an older spec version and must not be treated as terminal.
			if cond.ObservedGeneration < cr.Generation {
				logger.V(1).Info("skipping stale condition on CommittedResource",
					"name", name,
					"generation", cr.Generation,
					"conditionObservedGeneration", cond.ObservedGeneration,
					"reason", cond.Reason,
				)
				continue
			}

			switch {
			case cond.Status == metav1.ConditionTrue:
				logger.Info("CommittedResource accepted", "name", name)
				delete(pending, name)
			case cond.Status == metav1.ConditionFalse && cond.Reason == v1alpha1.CommittedResourceReasonPlanned:
				logger.Info("CommittedResource planned (will reserve at activation)", "name", name)
				delete(pending, name) // planned = accepted; controller will reserve at activation
			case cond.Status == metav1.ConditionFalse && cond.Reason == v1alpha1.CommittedResourceReasonRejected:
				logger.Info("CommittedResource rejected", "name", name, "reason", cond.Message)
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
//   - wasDeleted=true, prevSpec!=nil: CRD was deleted; re-create it from the snapshot.
//   - wasDeleted=true, prevSpec==nil: CRD was absent before and after; nothing to do.
//   - wasDeleted=false, prevSpec==nil: CRD was newly created; delete it.
//   - wasDeleted=false, prevSpec!=nil: CRD was updated; restore its spec.
func rollbackCR(ctx context.Context, logger logr.Logger, k8sClient client.Client, snap crSnapshot) {
	if snap.wasDeleted {
		if snap.prevSpec == nil {
			return // was absent before deletion attempt; nothing to undo
		}
		cr := &v1alpha1.CommittedResource{}
		cr.Name = snap.crName
		cr.Spec = *snap.prevSpec
		if err := k8sClient.Create(ctx, cr); client.IgnoreAlreadyExists(err) != nil {
			logger.Error(err, "failed to re-create CommittedResource CRD during rollback", "name", snap.crName)
		}
		return
	}

	if snap.prevSpec == nil {
		cr := &v1alpha1.CommittedResource{}
		cr.Name = snap.crName
		if err := k8sClient.Delete(ctx, cr); client.IgnoreNotFound(err) != nil {
			logger.Error(err, "failed to delete CommittedResource CRD during rollback", "name", snap.crName)
		}
		return
	}

	// The controller may write status (bumping resourceVersion) between our Get and Update.
	// RetryOnConflict retries with exponential backoff when that race occurs.
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cr := &v1alpha1.CommittedResource{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: snap.crName}, cr); err != nil {
			return err
		}
		cr.Spec = *snap.prevSpec
		return k8sClient.Update(ctx, cr)
	})
	if err != nil {
		logger.Error(err, "failed to restore CommittedResource CRD spec during rollback", "name", snap.crName)
		return
	}
	logger.V(1).Info("restored CommittedResource CRD spec during rollback", "name", snap.crName)
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
