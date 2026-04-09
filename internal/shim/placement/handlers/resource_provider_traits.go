// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"net/http"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// HandleListResourceProviderTraits handles
// GET /resource_providers/{uuid}/traits requests.
//
// Returns the list of traits associated with the resource provider identified
// by {uuid}. The response includes an array of trait name strings and the
// resource_provider_generation for concurrency tracking. Returns 404 if the
// provider does not exist.
func HandleListResourceProviderTraits(w http.ResponseWriter, r *http.Request) {
	uuid, ok := requiredUUIDPathParam(w, r, "uuid")
	if !ok {
		return
	}
	log := logf.FromContext(r.Context())
	log.Info("placement request", "method", r.Method, "path", r.URL.Path, "uuid", uuid)
}

// HandleUpdateResourceProviderTraits handles
// PUT /resource_providers/{uuid}/traits requests.
//
// Replaces the complete set of trait associations for a resource provider.
// The request body must include a traits array and the
// resource_provider_generation for optimistic concurrency control. All
// previously associated traits are removed and replaced by the specified set.
// Returns 400 Bad Request if any of the specified traits are invalid (i.e.
// not returned by GET /traits). Returns 409 Conflict if the generation does
// not match.
func HandleUpdateResourceProviderTraits(w http.ResponseWriter, r *http.Request) {
	uuid, ok := requiredUUIDPathParam(w, r, "uuid")
	if !ok {
		return
	}
	log := logf.FromContext(r.Context())
	log.Info("placement request", "method", r.Method, "path", r.URL.Path, "uuid", uuid)
}

// HandleDeleteResourceProviderTraits handles
// DELETE /resource_providers/{uuid}/traits requests.
//
// Removes all trait associations from a resource provider. Because this
// endpoint does not accept a resource_provider_generation, it is not safe
// for concurrent use. In environments where multiple clients manage traits
// for the same provider, prefer PUT with an empty traits list instead.
// Returns 404 if the provider does not exist. Returns 409 Conflict on
// concurrent modification. Returns 204 No Content on success.
func HandleDeleteResourceProviderTraits(w http.ResponseWriter, r *http.Request) {
	uuid, ok := requiredUUIDPathParam(w, r, "uuid")
	if !ok {
		return
	}
	log := logf.FromContext(r.Context())
	log.Info("placement request", "method", r.Method, "path", r.URL.Path, "uuid", uuid)
}
