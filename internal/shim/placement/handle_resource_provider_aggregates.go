// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"
)

// HandleListResourceProviderAggregates handles
// GET /resource_providers/{uuid}/aggregates requests.
//
// Returns the list of aggregate UUIDs associated with the resource provider.
// Aggregates model relationships among providers such as shared storage,
// affinity/anti-affinity groups, and availability zones. Returns an empty
// list if the provider has no aggregate associations.
//
// https://docs.openstack.org/api-ref/placement/#list-resource-provider-aggregates
func (s *Shim) HandleListResourceProviderAggregates(w http.ResponseWriter, r *http.Request) {
	if !s.config.Features.Aggregates {
		s.forward(w, r)
		return
	}
	if _, ok := requiredUUIDPathParam(w, r, "uuid"); !ok {
		return
	}
	http.Error(w, "not yet implemented", http.StatusNotImplemented)
}

// HandleUpdateResourceProviderAggregates handles
// PUT /resource_providers/{uuid}/aggregates requests.
//
// Replaces the complete set of aggregate associations for a resource provider.
// The request body must include an aggregates array and a
// resource_provider_generation for optimistic concurrency control. Returns
// 409 Conflict if the generation does not match. Returns 200 with the
// updated aggregate list on success.
//
// https://docs.openstack.org/api-ref/placement/#update-resource-provider-aggregates
func (s *Shim) HandleUpdateResourceProviderAggregates(w http.ResponseWriter, r *http.Request) {
	if !s.config.Features.Aggregates {
		s.forward(w, r)
		return
	}
	if _, ok := requiredUUIDPathParam(w, r, "uuid"); !ok {
		return
	}
	http.Error(w, "not yet implemented", http.StatusNotImplemented)
}
