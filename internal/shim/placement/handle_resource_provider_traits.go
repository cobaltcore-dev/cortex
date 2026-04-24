// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"fmt"
	"net/http"
)

// HandleListResourceProviderTraits handles
// GET /resource_providers/{uuid}/traits requests.
//
// Returns the list of traits associated with the resource provider identified
// by {uuid}. The response includes an array of trait name strings and the
// resource_provider_generation for concurrency tracking. Returns 404 if the
// provider does not exist.
func (s *Shim) HandleListResourceProviderTraits(w http.ResponseWriter, r *http.Request) {
	if _, ok := requiredUUIDPathParam(w, r, "uuid"); !ok {
		return
	}
	switch s.config.Features.ResourceProviderTraits.orDefault() {
	case FeatureModePassthrough:
		s.forward(w, r)
	case FeatureModeHybrid, FeatureModeCRD:
		http.Error(w, fmt.Sprintf("%s mode is not yet implemented for this endpoint", s.config.Features.ResourceProviderTraits), http.StatusNotImplemented)
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
	}
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
func (s *Shim) HandleUpdateResourceProviderTraits(w http.ResponseWriter, r *http.Request) {
	if _, ok := requiredUUIDPathParam(w, r, "uuid"); !ok {
		return
	}
	switch s.config.Features.ResourceProviderTraits.orDefault() {
	case FeatureModePassthrough:
		s.forward(w, r)
	case FeatureModeHybrid, FeatureModeCRD:
		http.Error(w, fmt.Sprintf("%s mode is not yet implemented for this endpoint", s.config.Features.ResourceProviderTraits), http.StatusNotImplemented)
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
	}
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
func (s *Shim) HandleDeleteResourceProviderTraits(w http.ResponseWriter, r *http.Request) {
	if _, ok := requiredUUIDPathParam(w, r, "uuid"); !ok {
		return
	}
	switch s.config.Features.ResourceProviderTraits.orDefault() {
	case FeatureModePassthrough:
		s.forward(w, r)
	case FeatureModeHybrid, FeatureModeCRD:
		http.Error(w, fmt.Sprintf("%s mode is not yet implemented for this endpoint", s.config.Features.ResourceProviderTraits), http.StatusNotImplemented)
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
	}
}
