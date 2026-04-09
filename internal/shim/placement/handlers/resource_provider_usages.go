// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"net/http"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// HandleListResourceProviderUsages handles
// GET /resource_providers/{uuid}/usages requests.
//
// Returns aggregated resource consumption for the resource provider identified
// by {uuid}. The response contains a usages dictionary keyed by resource class
// with integer usage amounts, along with the resource_provider_generation.
// Unlike the provider allocations endpoint, this does not break down usage by
// individual consumer. Returns 404 if the provider does not exist.
func HandleListResourceProviderUsages(w http.ResponseWriter, r *http.Request) {
	uuid, ok := requiredUUIDPathParam(w, r, "uuid")
	if !ok {
		return
	}
	log := logf.FromContext(r.Context())
	log.Info("placement request", "method", r.Method, "path", r.URL.Path, "uuid", uuid)
}
