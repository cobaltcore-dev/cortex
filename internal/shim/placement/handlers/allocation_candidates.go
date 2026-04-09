// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"net/http"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// HandleListAllocationCandidates handles GET /allocation_candidates requests.
//
// Returns a collection of allocation requests and resource provider summaries
// that can satisfy a given set of resource and trait requirements. This is the
// primary endpoint used by Nova's scheduler to find suitable hosts for
// instance placement.
//
// The resources query parameter specifies required capacity as a comma-
// separated list (e.g. VCPU:4,MEMORY_MB:2048,DISK_GB:64). The required
// parameter filters by traits, supporting forbidden traits via ! prefix
// (since 1.22) and the in: syntax for any-of semantics (since 1.39).
// The member_of parameter filters by aggregate membership with support for
// forbidden aggregates via ! prefix (since 1.32).
//
// Since microversion 1.25, granular request groups are supported via numbered
// suffixes (resourcesN, requiredN, member_ofN) to express requirements that
// may be satisfied by different providers. The group_policy parameter (1.26+)
// controls whether groups must each be satisfied by a single provider or may
// span multiple. The in_tree parameter (1.31+) constrains results to a
// specific provider tree.
//
// Each returned allocation request is directly usable as the body for
// PUT /allocations/{consumer_uuid}. The provider_summaries section includes
// inventory capacity and usage for informed decision-making. Available since
// microversion 1.10.
func HandleListAllocationCandidates(w http.ResponseWriter, r *http.Request) {
	log := logf.FromContext(r.Context())
	log.Info("placement request", "method", r.Method, "path", r.URL.Path)
}
