// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

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
	commitments "github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/commitments"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	liquid "github.com/sapcc/go-api-declarations/liquid"
)

// errInternalServiceInfo indicates an internal error while building service info (e.g., invalid unit configuration)
var errInternalServiceInfo = errors.New("internal error building service info")

// handles GET /commitments/v1/info requests from Limes:
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
	logger := commitments.LoggerFromContext(ctx).WithValues("component", "api", "endpoint", "/commitments/v1/info")

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
// For each flavor group, three resources are registered:
// - _ram: RAM resource (unit = multiples of smallest flavor RAM, HandlesCommitments=true only if fixed ratio)
// - _cores: CPU cores resource (unit = 1, HandlesCommitments=false)
// - _instances: Instance count resource (unit = 1, HandlesCommitments=false)
// All flavor groups report usage; only those with fixed RAM/core ratio accept commitments.
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
		// Determine if this group accepts commitments (requires fixed RAM/core ratio)
		handlesCommitments := commitments.FlavorGroupAcceptsCommitments(&groupData)

		// All flavor groups are registered for usage reporting.
		// Only those with a fixed RAM/core ratio have HandlesCommitments=true.

		flavorNames := make([]string, 0, len(groupData.Flavors))
		for _, flavor := range groupData.Flavors {
			flavorNames = append(flavorNames, flavor.Name)
		}
		flavorListStr := strings.Join(flavorNames, ", ")

		// Build attributes JSON with ratio info (shared across all resource types)
		attrs := resourceAttributes{
			RamCoreRatio:    groupData.RamCoreRatio,
			RamCoreRatioMin: groupData.RamCoreRatioMin,
			RamCoreRatioMax: groupData.RamCoreRatioMax,
		}
		attrsJSON, err := json.Marshal(attrs)
		if err != nil {
			logger.Error(err, "failed to marshal resource attributes", "flavorGroup", groupName)
			attrsJSON = nil
		}

		// Validate memory is positive to avoid panic in MultiplyBy (which panics on factor=0)
		if groupData.SmallestFlavor.MemoryMB == 0 {
			return liquid.ServiceInfo{}, fmt.Errorf("%w: flavor group %q has invalid smallest flavor with memoryMB=0",
				errInternalServiceInfo, groupName)
		}

		// === 1. RAM Resource ===
		ramResourceName := liquid.ResourceName(commitments.ResourceNameRAM(groupName))
		ramUnit, err := liquid.UnitMebibytes.MultiplyBy(groupData.SmallestFlavor.MemoryMB)
		if err != nil {
			// Note: This error only occurs on uint64 overflow, which is unrealistic for memory values
			return liquid.ServiceInfo{}, fmt.Errorf("%w: failed to create unit for flavor group %q: %w",
				errInternalServiceInfo, groupName, err)
		}
		// Determine topology: AZSeparatedTopology only for groups that accept commitments
		// (AZSeparatedTopology means quota is also AZ-aware, required when HasQuota=true)
		ramTopology := liquid.AZAwareTopology
		if handlesCommitments {
			ramTopology = liquid.AZSeparatedTopology
		}
		resources[ramResourceName] = liquid.ResourceInfo{
			DisplayName: fmt.Sprintf(
				"multiples of %d MiB (usable by: %s)",
				groupData.SmallestFlavor.MemoryMB,
				flavorListStr,
			),
			Unit:                ramUnit, // Non-standard unit: multiples of smallest flavor RAM
			Topology:            ramTopology,
			NeedsResourceDemand: false,
			HasCapacity:         true,               // We report capacity via /commitments/v1/report-capacity
			HasQuota:            handlesCommitments, // true only for groups that accept commitments
			HandlesCommitments:  handlesCommitments, // Only groups with fixed ratio accept commitments
			Attributes:          attrsJSON,
		}

		// === 2. Cores Resource ===
		coresResourceName := liquid.ResourceName(commitments.ResourceNameCores(groupName))
		resources[coresResourceName] = liquid.ResourceInfo{
			DisplayName: fmt.Sprintf(
				"CPU cores (usable by: %s)",
				flavorListStr,
			),
			Unit:                liquid.UnitNone,        // Countable unit (omitted in JSON = "1")
			Topology:            liquid.AZAwareTopology, // Same topology as RAM
			NeedsResourceDemand: false,
			HasCapacity:         true,      // We report capacity (as 0 for now)
			HasQuota:            false,     // No quota enforcement
			HandlesCommitments:  false,     // Cores are derived from RAM commitments
			Attributes:          attrsJSON, // Same attributes (ratio info)
		}

		// === 3. Instances Resource ===
		instancesResourceName := liquid.ResourceName(commitments.ResourceNameInstances(groupName))
		resources[instancesResourceName] = liquid.ResourceInfo{
			DisplayName: fmt.Sprintf(
				"instances (usable by: %s)",
				flavorListStr,
			),
			Unit:                liquid.UnitNone,        // Countable unit (omitted in JSON = "1")
			Topology:            liquid.AZAwareTopology, // Same topology as RAM
			NeedsResourceDemand: false,
			HasCapacity:         true,      // We report capacity (as 0 for now)
			HasQuota:            false,     // No quota enforcement
			HandlesCommitments:  false,     // Instances are derived from RAM commitments
			Attributes:          attrsJSON, // Same attributes
		}

		logger.V(1).Info("registered flavor group resources",
			"flavorGroup", groupName,
			"ramResource", ramResourceName,
			"coresResource", coresResourceName,
			"instancesResource", instancesResourceName,
			"smallestFlavor", groupData.SmallestFlavor.Name,
			"smallestRamMB", groupData.SmallestFlavor.MemoryMB,
			"ramCoreRatio", groupData.RamCoreRatio)
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
