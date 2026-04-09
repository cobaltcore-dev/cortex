// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"net/http"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// HandleListUsages handles GET /usages requests.
//
// Returns a report of aggregated resource usage for a given project, and
// optionally a specific user within that project. The project_id query
// parameter is required; user_id is optional.
//
// The response format changed at microversion 1.38: earlier versions return
// a flat dictionary of resource class to usage totals, while 1.38+ groups
// usages by consumer_type (e.g. INSTANCE, MIGRATION, all, unknown), with
// each group containing resource totals and a consumer_count. Since
// microversion 1.38, an optional consumer_type query parameter allows
// filtering the results. Available since microversion 1.9.
func HandleListUsages(w http.ResponseWriter, r *http.Request) {
	log := logf.FromContext(r.Context())
	log.Info("placement request", "method", r.Method, "path", r.URL.Path)
}
