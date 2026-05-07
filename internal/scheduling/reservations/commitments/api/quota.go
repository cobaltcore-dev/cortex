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
	"github.com/google/uuid"
	"github.com/sapcc/go-api-declarations/liquid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// projectQuotaCRDName returns the CRD object name for a given project UUID.
// Convention: "quota-<project-uuid>"
func projectQuotaCRDName(projectID string) string {
	return "quota-" + projectID
}

// HandleQuota implements PUT /commitments/v1/projects/:project_id/quota from Limes LIQUID API.
// See: https://pkg.go.dev/github.com/sapcc/go-api-declarations/liquid
//
// This endpoint receives quota requests from Limes and persists them as ProjectQuota CRDs.
// One CRD per project, named "quota-<project-uuid>".
func (api *HTTPAPI) HandleQuota(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	// Extract or generate request ID for tracing
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = uuid.New().String()
	}
	w.Header().Set("X-Request-ID", requestID)

	log := apiLog.WithValues("requestID", requestID, "endpoint", "quota")

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

	// Build the spec quota map from the liquid request.
	// liquid API uses uint64; our CRD uses int64 (K8s convention).
	// Guard against overflow: uint64 values > MaxInt64 would wrap to negative.
	specQuota := make(map[string]v1alpha1.ResourceQuota, len(req.Resources))
	for resourceName, resQuota := range req.Resources {
		if resQuota.Quota > math.MaxInt64 {
			api.quotaError(w, http.StatusBadRequest, fmt.Sprintf("Quota value for resource %q exceeds int64 max", resourceName), startTime)
			return
		}
		rq := v1alpha1.ResourceQuota{
			Quota: int64(resQuota.Quota),
		}
		if len(resQuota.PerAZ) > 0 {
			rq.PerAZ = make(map[string]int64, len(resQuota.PerAZ))
			for az, azQuota := range resQuota.PerAZ {
				if azQuota.Quota > math.MaxInt64 {
					api.quotaError(w, http.StatusBadRequest, fmt.Sprintf("Quota value for resource %q in AZ %q exceeds int64 max", resourceName, az), startTime)
					return
				}
				rq.PerAZ[string(az)] = int64(azQuota.Quota)
			}
		}
		specQuota[string(resourceName)] = rq
	}

	// Create or update ProjectQuota CRD with retry-on-conflict to handle
	// concurrent status updates from the quota controller.
	crdName := projectQuotaCRDName(projectID)
	ctx := r.Context()

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
					ProjectID:   projectID,
					ProjectName: projectName,
					DomainID:    domainID,
					DomainName:  domainName,
					Quota:       specQuota,
				},
			}
			if createErr := api.client.Create(ctx, pq); createErr != nil {
				// If another request just created it, retry will fetch and update
				if apierrors.IsAlreadyExists(createErr) {
					return createErr
				}
				return createErr
			}
			log.V(1).Info("created ProjectQuota", "name", crdName, "projectID", projectID, "resources", len(specQuota))
			return nil
		}

		// Update existing (re-fetched on each retry to get fresh resourceVersion)
		existing.Spec.Quota = specQuota
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
		log.V(1).Info("updated ProjectQuota", "name", crdName, "projectID", projectID, "resources", len(specQuota))
		return nil
	})
	if err != nil {
		log.Error(err, "failed to create/update ProjectQuota", "name", crdName)
		api.quotaError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to persist quota: %v", err), startTime)
		return
	}

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
