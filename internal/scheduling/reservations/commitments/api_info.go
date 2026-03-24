// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/go-logr/logr"
	liquid "github.com/sapcc/go-api-declarations/liquid"
)

// handles GET /v1/info requests from Limes:
// See: https://github.com/sapcc/go-api-declarations/blob/main/liquid/commitment.go
// See: https://pkg.go.dev/github.com/sapcc/go-api-declarations/liquid
func (api *HTTPAPI) HandleInfo(w http.ResponseWriter, r *http.Request) {
	ctx := WithNewGlobalRequestID(r.Context())
	logger := LoggerFromContext(ctx).WithValues("component", "api", "endpoint", "/v1/info")

	// Only accept GET method
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	logger.V(1).Info("processing info request")

	// Build info response
	info, err := api.buildServiceInfo(ctx, logger)
	if err != nil {
		// Use Info level for expected conditions like knowledge not being ready yet
		logger.Info("service info not available yet", "error", err.Error())
		http.Error(w, "Service temporarily unavailable: "+err.Error(),
			http.StatusServiceUnavailable)
		return
	}

	// Return response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(info); err != nil {
		logger.Error(err, "failed to encode service info")
		return
	}
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

		resources[resourceName] = liquid.ResourceInfo{
			DisplayName:         displayName,
			Unit:                liquid.UnitNone,        // Countable: multiples of smallest flavor instances
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
