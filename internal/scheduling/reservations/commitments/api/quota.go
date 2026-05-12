// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	commitments "github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/commitments"
	"github.com/google/uuid"
	"github.com/sapcc/go-api-declarations/liquid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// idxProjectQuotaByProjectID is the field index key used to look up ProjectQuota CRDs by project ID.
// Must match the index registered in field_index.go.
const idxProjectQuotaByProjectID = "spec.projectID"

// projectQuotaCRDName returns the CRD object name for a given project UUID and AZ.
// Convention: "quota-<project-uuid>-<az>"
func projectQuotaCRDName(projectID, az string) string {
	return "quota-" + projectID + "-" + az
}

// HandleQuota implements PUT /commitments/v1/projects/:project_id/quota from Limes LIQUID API.
// See: https://pkg.go.dev/github.com/sapcc/go-api-declarations/liquid
//
// This endpoint receives quota requests from Limes and persists them as ProjectQuota CRDs.
// One CRD per project per availability zone, named "quota-<project-uuid>-<az>".
func (api *HTTPAPI) HandleQuota(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	// Extract or generate request ID for tracing
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = uuid.New().String()
	}
	w.Header().Set("X-Request-ID", requestID)

	log := apiLog.WithValues("requestID", requestID, "endpoint", "quota", "module", "quota-handling")
	log.Info("received quota request", "method", r.Method, "path", r.URL.Path)

	if r.Method != http.MethodPut {
		api.quotaError(w, http.StatusMethodNotAllowed, "Method not allowed", startTime)
		return
	}

	// Check if quota API is enabled
	if !api.config.EnableQuotaAPI {
		api.quotaError(w, http.StatusServiceUnavailable, "Quota API is disabled", startTime)
		return
	}

	// Extract project UUID from URL path
	projectID, err := extractProjectIDFromPath(r.URL.Path)
	if err != nil {
		log.Error(err, "failed to extract project ID from path")
		api.quotaError(w, http.StatusBadRequest, "Invalid URL path: "+err.Error(), startTime)
		return
	}

	// Parse request body
	var req liquid.ServiceQuotaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Error(err, "failed to decode quota request body")
		api.quotaError(w, http.StatusBadRequest, "Invalid request body: "+err.Error(), startTime)
		return
	}

	// Extract project/domain metadata if available
	var projectName, domainID, domainName string
	if meta, ok := req.ProjectMetadata.Unpack(); ok {
		// Consistency check: metadata UUID must match URL path UUID
		if meta.UUID != "" && meta.UUID != projectID {
			log.Info("project UUID mismatch", "urlProjectID", projectID, "metadataUUID", meta.UUID)
			api.quotaError(w, http.StatusBadRequest, fmt.Sprintf("Project UUID mismatch: URL has %q but metadata has %q", projectID, meta.UUID), startTime)
			return
		}
		projectName = meta.Name
		domainID = meta.Domain.UUID
		domainName = meta.Domain.Name
	}

	if domainID == "" {
		api.quotaError(w, http.StatusBadRequest, "missing domain UUID in project metadata", startTime)
		return
	}

	// Fetch flavor groups to determine per-resource RAM unit.
	// The ProjectQuota CRD stores RAM values in GiB; Limes sends in the declared unit
	// (slots for fixed-ratio groups, GiB for variable-ratio). Convert on receipt.
	knowledge := &reservations.FlavorGroupKnowledgeClient{Client: api.client}
	flavorGroups, err := knowledge.GetAllFlavorGroups(r.Context(), nil)
	if err != nil {
		log.Info("flavor groups not available for quota unit conversion", "error", err.Error())
		api.quotaError(w, http.StatusServiceUnavailable, "flavor groups not available: "+err.Error(), startTime)
		return
	}
	// ramResourceToGroup maps RAM resource name → group name for unit conversion.
	ramResourceToGroup := make(map[string]string, len(flavorGroups))
	for groupName := range flavorGroups {
		ramResourceToGroup[commitments.ResourceNameRAM(groupName)] = groupName
	}

	// Build per-AZ quota maps from the liquid request, converting RAM to GiB.
	// liquid API uses uint64; our CRD uses int64 (K8s convention).
	// Guard against overflow: uint64 values > MaxInt64 would wrap to negative.
	// quotaByAZ[az][resourceName] = quota in GiB for that AZ
	quotaByAZ := make(map[string]map[string]int64)
	for resourceName, resQuota := range req.Resources {
		for az, azQuota := range resQuota.PerAZ {
			if azQuota.Quota > math.MaxInt64 {
				api.quotaError(w, http.StatusBadRequest, fmt.Sprintf("Quota value for resource %q in AZ %q exceeds int64 max", resourceName, az), startTime)
				return
			}
			quotaValue := int64(azQuota.Quota)
			if groupName, ok := ramResourceToGroup[string(resourceName)]; ok {
				fg := flavorGroups[groupName]
				quotaValue = fg.DeclaredUnitsToGiB(quotaValue)
			}
			azStr := string(az)
			if quotaByAZ[azStr] == nil {
				quotaByAZ[azStr] = make(map[string]int64)
			}
			quotaByAZ[azStr][string(resourceName)] = quotaValue
		}
	}

	ctx := r.Context()

	// Create or update one ProjectQuota CRD per AZ with retry-on-conflict to handle
	// concurrent status updates from the quota controller.
	activeAZs := make(map[string]bool, len(quotaByAZ))
	for az, azQuota := range quotaByAZ {
		activeAZs[az] = true
		crdName := projectQuotaCRDName(projectID, az)

		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var existing v1alpha1.ProjectQuota
			getErr := api.client.Get(ctx, client.ObjectKey{Name: crdName}, &existing)
			if getErr != nil {
				if !apierrors.IsNotFound(getErr) {
					return getErr
				}
				// Not found -- create new
				pq := &v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{
						Name: crdName,
					},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        projectID,
						ProjectName:      projectName,
						DomainID:         domainID,
						DomainName:       domainName,
						AvailabilityZone: az,
						Quota:            azQuota,
					},
				}
				if createErr := api.client.Create(ctx, pq); createErr != nil {
					// If another request just created it, surface as a conflict so
					// RetryOnConflict re-runs the closure and falls into the update branch.
					if apierrors.IsAlreadyExists(createErr) {
						return apierrors.NewConflict(
							schema.GroupResource{Group: "cortex.cloud", Resource: "projectquotas"},
							crdName, createErr,
						)
					}
					return createErr
				}
				log.V(1).Info("created ProjectQuota", "name", crdName, "projectID", projectID, "az", az, "resources", len(azQuota))
				return nil
			}

			// Update existing (re-fetched on each retry to get fresh resourceVersion)
			existing.Spec.Quota = azQuota
			if projectName != "" {
				existing.Spec.ProjectName = projectName
			}
			if domainID != "" {
				existing.Spec.DomainID = domainID
			}
			if domainName != "" {
				existing.Spec.DomainName = domainName
			}
			if updateErr := api.client.Update(ctx, &existing); updateErr != nil {
				return updateErr
			}
			log.V(1).Info("updated ProjectQuota", "name", crdName, "projectID", projectID, "az", az, "resources", len(azQuota))
			return nil
		})
		if err != nil {
			log.Error(err, "failed to create/update ProjectQuota", "name", crdName, "az", az)
			api.quotaError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to persist quota for AZ %s: %v", az, err), startTime)
			return
		}
	}

	// Delete orphan CRDs for AZs no longer present in the quota push.
	var pqList v1alpha1.ProjectQuotaList
	if err := api.client.List(ctx, &pqList, client.MatchingFields{idxProjectQuotaByProjectID: projectID}); err == nil {
		for i := range pqList.Items {
			pq := &pqList.Items[i]
			if !activeAZs[pq.Spec.AvailabilityZone] {
				if delErr := api.client.Delete(ctx, pq); delErr != nil && !apierrors.IsNotFound(delErr) {
					log.Error(delErr, "failed to delete orphan ProjectQuota", "name", pq.Name, "az", pq.Spec.AvailabilityZone)
					// Non-fatal: orphan will be cleaned up on next push
				} else {
					log.V(1).Info("deleted orphan ProjectQuota", "name", pq.Name, "az", pq.Spec.AvailabilityZone)
				}
			}
		}
	}

	// Collect AZ names for the success log
	azNames := make([]string, 0, len(activeAZs))
	for az := range activeAZs {
		azNames = append(azNames, az)
	}
	log.Info("quota request completed", "projectID", projectID, "azs", azNames)

	// Return 204 No Content as expected by the LIQUID API
	w.WriteHeader(http.StatusNoContent)
	api.recordQuotaMetrics(http.StatusNoContent, startTime)
}

// quotaError writes an HTTP error response and records metrics. Used for error paths in HandleQuota.
func (api *HTTPAPI) quotaError(w http.ResponseWriter, statusCode int, msg string, startTime time.Time) {
	http.Error(w, msg, statusCode)
	api.recordQuotaMetrics(statusCode, startTime)
}

// recordQuotaMetrics records Prometheus metrics for a quota API request.
func (api *HTTPAPI) recordQuotaMetrics(statusCode int, startTime time.Time) {
	duration := time.Since(startTime).Seconds()
	statusCodeStr := strconv.Itoa(statusCode)
	api.quotaMonitor.requestCounter.WithLabelValues(statusCodeStr).Inc()
	api.quotaMonitor.requestDuration.WithLabelValues(statusCodeStr).Observe(duration)
}
