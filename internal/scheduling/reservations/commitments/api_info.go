// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	liquid "github.com/sapcc/go-api-declarations/liquid"
)

// errInternalServiceInfo indicates an internal error while building service info (e.g., invalid unit configuration)
var errInternalServiceInfo = errors.New("internal error building service info")

// handles GET /v1/info requests from Limes:
// See: https://github.com/sapcc/go-api-declarations/blob/main/liquid/commitment.go
// See: https://pkg.go.dev/github.com/sapcc/go-api-declarations/liquid
func (api *HTTPAPI) HandleInfo(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	statusCode := http.StatusOK

	// Extract or generate request ID for tracing
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = uuid.New().String()
	}
	// Set request ID in response header for client correlation
	w.Header().Set("X-Request-ID", requestID)

	ctx := reservations.WithGlobalRequestID(r.Context(), "committed-resource-"+requestID)
	logger := LoggerFromContext(ctx).WithValues("component", "api", "endpoint", "/v1/info")

	// Only accept GET method
	if r.Method != http.MethodGet {
		statusCode = http.StatusMethodNotAllowed
		http.Error(w, "Method not allowed", statusCode)
		api.recordInfoMetrics(statusCode, startTime)
		return
	}

	logger.V(1).Info("processing info request")

	// Build info response
	info, err := api.buildServiceInfo(ctx, logger)
	if err != nil {
		if errors.Is(err, errInternalServiceInfo) {
			logger.Error(err, "internal error building service info")
			statusCode = http.StatusInternalServerError
			http.Error(w, "Internal server error: "+err.Error(), statusCode)
		} else {
			// Use Info level for expected conditions like knowledge not being ready yet
			logger.Info("service info not available yet", "error", err.Error())
			statusCode = http.StatusServiceUnavailable
			http.Error(w, "Service temporarily unavailable: "+err.Error(), statusCode)
		}
		api.recordInfoMetrics(statusCode, startTime)
		return
	}

	// Return response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(info); err != nil {
		logger.Error(err, "failed to encode service info")
	}
	api.recordInfoMetrics(statusCode, startTime)
}

// recordInfoMetrics records Prometheus metrics for an info API request.
func (api *HTTPAPI) recordInfoMetrics(statusCode int, startTime time.Time) {
	duration := time.Since(startTime).Seconds()
	statusCodeStr := strconv.Itoa(statusCode)
	api.infoMonitor.requestCounter.WithLabelValues(statusCodeStr).Inc()
	api.infoMonitor.requestDuration.WithLabelValues(statusCodeStr).Observe(duration)
}

// resourceAttributes holds the custom attributes for a resource in the info API response.
type resourceAttributes struct {
	RamCoreRatio    *uint64 `json:"ramCoreRatio,omitempty"`
	RamCoreRatioMin *uint64 `json:"ramCoreRatioMin,omitempty"`
	RamCoreRatioMax *uint64 `json:"ramCoreRatioMax,omitempty"`
}

// buildServiceInfo constructs the ServiceInfo response with metadata for all flavor groups.
func (api *HTTPAPI) buildServiceInfo(ctx context.Context, logger logr.Logger) (liquid.ServiceInfo, error) {
	// Get all flavor groups from Knowledge CRDs
	knowledge := &reservations.FlavorGroupKnowledgeClient{Client: api.client}
	flavorGroups, err := knowledge.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		// Return -1 as version when knowledge is not ready
		return liquid.ServiceInfo{
			Version:   -1,
			Resources: make(map[liquid.ResourceName]liquid.ResourceInfo),
		}, err
	}

	// Build resources map
	resources := make(map[liquid.ResourceName]liquid.ResourceInfo)
	for groupName, groupData := range flavorGroups {
		resourceName := liquid.ResourceName(commitmentResourceNamePrefix + groupName)

		flavorNames := make([]string, 0, len(groupData.Flavors))
		for _, flavor := range groupData.Flavors {
			flavorNames = append(flavorNames, flavor.Name)
		}
		displayName := fmt.Sprintf(
			"multiples of %d MiB (usable by: %s)",
			groupData.SmallestFlavor.MemoryMB,
			strings.Join(flavorNames, ", "),
		)

		// Only handle commitments for groups with a fixed RAM/core ratio
		handlesCommitments := FlavorGroupAcceptsCommitments(&groupData)

		// Build attributes JSON with ratio info
		attrs := resourceAttributes{
			RamCoreRatio:    groupData.RamCoreRatio,
			RamCoreRatioMin: groupData.RamCoreRatioMin,
			RamCoreRatioMax: groupData.RamCoreRatioMax,
		}
		attrsJSON, err := json.Marshal(attrs)
		if err != nil {
			logger.Error(err, "failed to marshal resource attributes", "resourceName", resourceName)
			attrsJSON = nil
		}

		// Build unit from smallest flavor memory (e.g., "131072 MiB" for 128 GiB)
		// Validate memory is positive to avoid panic in MultiplyBy (which panics on factor=0)
		if groupData.SmallestFlavor.MemoryMB == 0 {
			return liquid.ServiceInfo{}, fmt.Errorf("%w: flavor group %q has invalid smallest flavor with memoryMB=0",
				errInternalServiceInfo, groupName)
		}
		unit, err := liquid.UnitMebibytes.MultiplyBy(groupData.SmallestFlavor.MemoryMB)
		if err != nil {
			// Note: This error only occurs on uint64 overflow, which is unrealistic for memory values
			return liquid.ServiceInfo{}, fmt.Errorf("%w: failed to create unit for flavor group %q: %w",
				errInternalServiceInfo, groupName, err)
		}

		resources[resourceName] = liquid.ResourceInfo{
			DisplayName:         displayName,
			Unit:                unit,                   // Non-standard unit: multiples of smallest flavor RAM
			Topology:            liquid.AZAwareTopology, // Commitments are per-AZ
			NeedsResourceDemand: false,                  // Capacity planning out of scope for now
			HasCapacity:         handlesCommitments,     // We report capacity via /v1/report-capacity only for groups that accept commitments
			HasQuota:            false,                  // No quota enforcement as of now
			HandlesCommitments:  handlesCommitments,     // Only for groups with fixed RAM/core ratio
			Attributes:          attrsJSON,
		}

		logger.V(1).Info("registered flavor group resource",
			"resourceName", resourceName,
			"flavorGroup", groupName,
			"displayName", displayName,
			"smallestFlavor", groupData.SmallestFlavor.Name,
			"smallestRamMB", groupData.SmallestFlavor.MemoryMB,
			"handlesCommitments", handlesCommitments,
			"ramCoreRatio", groupData.RamCoreRatio,
			"ramCoreRatioMin", groupData.RamCoreRatioMin,
			"ramCoreRatioMax", groupData.RamCoreRatioMax)
	}

	// Get last content changed from flavor group knowledge and treat it as version
	var version int64 = -1
	if knowledgeCRD, err := knowledge.Get(ctx); err == nil && knowledgeCRD != nil && !knowledgeCRD.Status.LastContentChange.IsZero() {
		version = knowledgeCRD.Status.LastContentChange.Unix()
	}

	logger.Info("built service info",
		"resourceCount", len(resources),
		"version", version)

	return liquid.ServiceInfo{
		Version:   version,
		Resources: resources,
	}, nil
}
