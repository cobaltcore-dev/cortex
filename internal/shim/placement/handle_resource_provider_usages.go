// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"fmt"
	"net/http"
)

// HandleListResourceProviderUsages handles
// GET /resource_providers/{uuid}/usages requests.
//
// Returns aggregated resource consumption for the resource provider identified
// by {uuid}. The response contains a usages dictionary keyed by resource class
// with integer usage amounts, along with the resource_provider_generation.
// Unlike the provider allocations endpoint, this does not break down usage by
// individual consumer. Returns 404 if the provider does not exist.
func (s *Shim) HandleListResourceProviderUsages(w http.ResponseWriter, r *http.Request) {
	if _, ok := requiredUUIDPathParam(w, r, "uuid"); !ok {
		return
	}
	switch s.config.Features.Usages.orDefault() {
	case FeatureModePassthrough:
		s.forward(w, r)
	case FeatureModeHybrid, FeatureModeCRD:
		http.Error(w, fmt.Sprintf("%s mode is not yet implemented for this endpoint", s.config.Features.Usages), http.StatusNotImplemented)
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
	}
}
