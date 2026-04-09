// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"net/http"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// HandleListResourceProviderAllocations handles
// GET /resource_providers/{uuid}/allocations requests.
//
// Returns all allocations made against the resource provider identified by
// {uuid}, keyed by consumer UUID. This provides a provider-centric view of
// consumption, complementing the consumer-centric GET /allocations/{consumer}
// endpoint. The response includes the resource_provider_generation. Returns
// 404 if the provider does not exist.
func HandleListResourceProviderAllocations(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	log := logf.FromContext(r.Context())
	log.Info("placement request", "method", r.Method, "path", r.URL.Path, "uuid", uuid)
}
