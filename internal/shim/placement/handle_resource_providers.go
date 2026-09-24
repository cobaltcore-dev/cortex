// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"
)

// HandleListResourceProviders handles GET /resource_providers requests.
//
// Returns a filtered list of resource providers. Resource providers are
// entities that provide consumable inventory of one or more classes of
// resources (e.g. a compute node providing VCPU, MEMORY_MB, DISK_GB).
//
// Supports numerous filter parameters including name, uuid, member_of
// (aggregate membership), resources (capacity filtering), in_tree (provider
// tree membership), and required (trait filtering).
//
// See: https://docs.openstack.org/api-ref/placement/#list-resource-providers
func (s *Shim) HandleListResourceProviders(w http.ResponseWriter, r *http.Request) {
	if !s.config.Features.ResourceProviders {
		s.forward(w, r)
		return
	}
	http.Error(w, "not yet implemented", http.StatusNotImplemented)
}

// HandleCreateResourceProvider handles POST /resource_providers requests.
//
// Creates a new resource provider. The request must include a name and may
// optionally specify a UUID and a parent_provider_uuid (since 1.14) to place
// the provider in a hierarchical tree. If no UUID is supplied, one is
// generated. Returns 409 Conflict if a provider with the same name or UUID
// already exists.
//
// See: https://docs.openstack.org/api-ref/placement/#create-resource-provider
func (s *Shim) HandleCreateResourceProvider(w http.ResponseWriter, r *http.Request) {
	if !s.config.Features.ResourceProviders {
		s.forward(w, r)
		return
	}
	http.Error(w, "not yet implemented", http.StatusNotImplemented)
}

// HandleShowResourceProvider handles GET /resource_providers/{uuid} requests.
//
// Returns a single resource provider identified by its UUID. The response
// includes the provider's name, generation (used for concurrency control in
// subsequent updates), and links. Returns 404 if the provider does not exist.
//
// See: https://docs.openstack.org/api-ref/placement/#show-resource-provider
func (s *Shim) HandleShowResourceProvider(w http.ResponseWriter, r *http.Request) {
	if !s.config.Features.ResourceProviders {
		s.forward(w, r)
		return
	}
	if _, ok := requiredUUIDPathParam(w, r, "uuid"); !ok {
		return
	}
	http.Error(w, "not yet implemented", http.StatusNotImplemented)
}

// HandleUpdateResourceProvider handles PUT /resource_providers/{uuid} requests.
//
// Updates a resource provider's name and, starting at microversion 1.14, its
// parent_provider_uuid. Returns 409 Conflict if another provider already has
// the requested name.
//
// See: https://docs.openstack.org/api-ref/placement/#update-resource-provider
func (s *Shim) HandleUpdateResourceProvider(w http.ResponseWriter, r *http.Request) {
	if !s.config.Features.ResourceProviders {
		s.forward(w, r)
		return
	}
	if _, ok := requiredUUIDPathParam(w, r, "uuid"); !ok {
		return
	}
	http.Error(w, "not yet implemented", http.StatusNotImplemented)
}

// HandleDeleteResourceProvider handles DELETE /resource_providers/{uuid} requests.
//
// Deletes a resource provider and disassociates all its aggregates and
// inventories. The operation fails with 409 Conflict if there are any
// allocations against the provider's inventories or if the provider has
// child providers in a tree hierarchy. Returns 204 No Content on success.
//
// See: https://docs.openstack.org/api-ref/placement/#delete-resource-provider
func (s *Shim) HandleDeleteResourceProvider(w http.ResponseWriter, r *http.Request) {
	if !s.config.Features.ResourceProviders {
		s.forward(w, r)
		return
	}
	if _, ok := requiredUUIDPathParam(w, r, "uuid"); !ok {
		return
	}
	http.Error(w, "not yet implemented", http.StatusNotImplemented)
}
