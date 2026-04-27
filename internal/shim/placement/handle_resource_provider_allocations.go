// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"
)

// HandleListResourceProviderAllocations handles
// GET /resource_providers/{uuid}/allocations requests.
//
// Returns all allocations made against the resource provider identified by
// {uuid}, keyed by consumer UUID. This provides a provider-centric view of
// consumption, complementing the consumer-centric GET /allocations/{consumer}
// endpoint. The response includes the resource_provider_generation. Returns
// 404 if the provider does not exist.
func (s *Shim) HandleListResourceProviderAllocations(w http.ResponseWriter, r *http.Request) {
	if _, ok := requiredUUIDPathParam(w, r, "uuid"); !ok {
		return
	}
	s.dispatchPassthroughOnly(w, r, s.config.Features.Allocations)
}
