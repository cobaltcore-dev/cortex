// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"net/http"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// HandleListResourceProviderInventories handles
// GET /resource_providers/{uuid}/inventories requests.
//
// Returns all inventory records for the resource provider identified by
// {uuid}. The response contains an inventories dictionary keyed by resource
// class, with each entry describing capacity constraints: total, reserved,
// min_unit, max_unit, step_size, and allocation_ratio. Also returns the
// resource_provider_generation, which is needed for subsequent update or
// delete operations. Returns 404 if the provider does not exist.
func HandleListResourceProviderInventories(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	log := logf.FromContext(r.Context())
	log.Info("placement request", "method", r.Method, "path", r.URL.Path, "uuid", uuid)
}

// HandleUpdateResourceProviderInventories handles
// PUT /resource_providers/{uuid}/inventories requests.
//
// Atomically replaces the entire set of inventory records for a provider.
// The request must include the resource_provider_generation for optimistic
// concurrency control — if the generation does not match, the request fails
// with 409 Conflict. The inventories field is a dictionary keyed by resource
// class, each specifying at minimum a total value. Omitted inventory classes
// are deleted. Returns 409 Conflict if allocations exceed the new capacity
// or if a concurrent update has occurred.
func HandleUpdateResourceProviderInventories(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	log := logf.FromContext(r.Context())
	log.Info("placement request", "method", r.Method, "path", r.URL.Path, "uuid", uuid)
}

// HandleDeleteResourceProviderInventories handles
// DELETE /resource_providers/{uuid}/inventories requests.
//
// Deletes all inventory records for a resource provider. This operation is
// not safe for concurrent use; the recommended alternative for concurrent
// environments is PUT with an empty inventories dictionary. Returns 409
// Conflict if allocations exist against any of the provider's inventories.
// Returns 404 if the provider does not exist. Available since microversion
// 1.5.
func HandleDeleteResourceProviderInventories(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	log := logf.FromContext(r.Context())
	log.Info("placement request", "method", r.Method, "path", r.URL.Path, "uuid", uuid)
}

// HandleShowResourceProviderInventory handles
// GET /resource_providers/{uuid}/inventories/{resource_class} requests.
//
// Returns a single inventory record for one resource class on the specified
// provider. The response includes total, reserved, min_unit, max_unit,
// step_size, allocation_ratio, and the resource_provider_generation. Returns
// 404 if the provider or inventory for that class does not exist.
func HandleShowResourceProviderInventory(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	resourceClass := r.PathValue("resource_class")
	log := logf.FromContext(r.Context())
	log.Info("placement request", "method", r.Method, "path", r.URL.Path,
		"uuid", uuid, "resource_class", resourceClass)
}

// HandleUpdateResourceProviderInventory handles
// PUT /resource_providers/{uuid}/inventories/{resource_class} requests.
//
// Creates or replaces the inventory record for a single resource class on
// the provider. The request must include resource_provider_generation for
// concurrency control and a total value. Optional fields control allocation
// constraints (allocation_ratio, min_unit, max_unit, step_size, reserved).
// Since microversion 1.26, the reserved value must not exceed total. Returns
// 409 Conflict on generation mismatch or if allocations would be violated.
func HandleUpdateResourceProviderInventory(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	resourceClass := r.PathValue("resource_class")
	log := logf.FromContext(r.Context())
	log.Info("placement request", "method", r.Method, "path", r.URL.Path,
		"uuid", uuid, "resource_class", resourceClass)
}

// HandleDeleteResourceProviderInventory handles
// DELETE /resource_providers/{uuid}/inventories/{resource_class} requests.
//
// Deletes the inventory record for a specific resource class on the provider.
// Returns 409 Conflict if allocations exist against this provider and resource
// class combination, or if a concurrent update has occurred. Returns 404 if
// the provider or inventory does not exist. Returns 204 No Content on success.
func HandleDeleteResourceProviderInventory(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	resourceClass := r.PathValue("resource_class")
	log := logf.FromContext(r.Context())
	log.Info("placement request", "method", r.Method, "path", r.URL.Path,
		"uuid", uuid, "resource_class", resourceClass)
}
