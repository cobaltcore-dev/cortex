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

// handles GET /v1/info requests from Limes:
// See: https://github.com/sapcc/go-api-declarations/blob/main/liquid/info.go
// See: https://github.com/sapcc/limes/blob/master/docs/operators/liquid.md
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
		// Use Info level for expected conditions like knowledge not being ready yet
		log.Info("failed to build service info", "error", err.Error())
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
		resourceName := liquid.ResourceName("ram_" + groupName)

		flavorNames := make([]string, 0, len(groupData.Flavors))
		for _, flavor := range groupData.Flavors {
			flavorNames = append(flavorNames, flavor.Name)
		}
		displayName := fmt.Sprintf(
			"multiples of %d MiB (usable by: %s)",
			groupData.SmallestFlavor.MemoryMB,
			strings.Join(flavorNames, ", "),
		)

		resources[resourceName] = liquid.ResourceInfo{
			DisplayName:         displayName,
			Unit:                liquid.UnitNone,        // Countable: multiples of smallest flavor instances
			Topology:            liquid.AZAwareTopology, // Commitments are per-AZ
			NeedsResourceDemand: false,                  // Capacity planning out of scope for now
			HasCapacity:         true,                   // We report capacity via /v1/report-capacity
			HasQuota:            false,                  // No quota enforcement as of now
			HandlesCommitments:  true,                   // We handle commitment changes via /v1/change-commitments
		}

		log.V(1).Info("registered flavor group resource",
			"resourceName", resourceName,
			"flavorGroup", groupName,
			"displayName", displayName,
			"smallestFlavor", groupData.SmallestFlavor.Name,
			"smallestRamMB", groupData.SmallestFlavor.MemoryMB)
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
