// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/go-logr/logr"
	liquid "github.com/sapcc/go-api-declarations/liquid"
)

// HandleInfo handles GET /v1/info requests from Limes.
// Returns metadata about available resources (flavor groups).
func (api *HTTPAPI) HandleInfo(w http.ResponseWriter, r *http.Request) {
	// Extract or generate request ID for tracing
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	log := commitmentApiLog.WithValues("requestID", requestID, "endpoint", "/v1/info")

	// Only accept GET method
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	log.V(1).Info("processing info request")

	// Build info response
	info, err := api.buildServiceInfo(r.Context(), log)
	if err != nil {
		log.Error(err, "failed to build service info")
		http.Error(w, "Failed to build service info: "+err.Error(),
			http.StatusInternalServerError)
		return
	}

	// Return response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(info); err != nil {
		log.Error(err, "failed to encode service info")
		return
	}
}

// buildServiceInfo constructs the ServiceInfo response with metadata for all flavor groups.
func (api *HTTPAPI) buildServiceInfo(ctx context.Context, log logr.Logger) (liquid.ServiceInfo, error) {
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
		// Resource name follows pattern: ram_<flavorgroup>
		resourceName := liquid.ResourceName("ram_" + groupName)

		// Calculate unit based on smallest flavor's RAM (in MB)
		// Unit represents the multiple of the smallest flavor
		smallestRAM := groupData.SmallestFlavor.MemoryMB
		unit := fmt.Sprintf("%d MiB", smallestRAM)

		// Build flavor names list for display
		flavorNames := make([]string, len(groupData.Flavors))
		for i, flavor := range groupData.Flavors {
			flavorNames[i] = flavor.Name
		}

		resources[resourceName] = liquid.ResourceInfo{
			DisplayName:         strings.Join(flavorNames, ", "), // join all flavor names within the group
			Unit:                liquid.UnitMebibytes,            // RAM is measured in MiB
			Topology:            liquid.AZAwareTopology,          // Commitments are per-AZ
			NeedsResourceDemand: false,                           // Capacity planning out of scope for now
			HasCapacity:         true,                            // We report capacity via /v1/report-capacity
			HasQuota:            false,                           // No quota enforcement as of now
		}

		log.V(1).Info("registered flavor group resource",
			"resourceName", resourceName,
			"flavorGroup", groupName,
			"unit", unit,
			"smallestFlavor", groupData.SmallestFlavor.Name,
			"smallestRamMB", smallestRAM)
	}

	// Get last content changed from flavor group knowledge and treat it as version
	var version int64 = -1
	if knowledgeCRD, err := knowledge.Get(ctx); err == nil && knowledgeCRD != nil && !knowledgeCRD.Status.LastContentChange.IsZero() {
		version = knowledgeCRD.Status.LastContentChange.Unix()
	}

	log.Info("built service info",
		"resourceCount", len(resources),
		"version", version)

	return liquid.ServiceInfo{
		Version:   version,
		Resources: resources,
	}, nil
}
