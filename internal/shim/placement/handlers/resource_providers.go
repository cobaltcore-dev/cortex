// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"net/http"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// HandleListResourceProviders handles GET /resource_providers requests.
//
// Returns a filtered list of resource providers. Resource providers are
// entities that provide consumable inventory of one or more classes of
// resources (e.g. a compute node providing VCPU, MEMORY_MB, DISK_GB).
//
// Supports numerous filter parameters including name, uuid, member_of
// (aggregate membership), resources (capacity filtering), in_tree (provider
// tree membership), and required (trait filtering). Multiple filters are
// combined with boolean AND logic. Many of these filters were added in later
// microversions: resources filtering at 1.3, tree queries at 1.14, trait
// requirements at 1.18, forbidden traits at 1.22, forbidden aggregates at
// 1.32, and the in: syntax for required at 1.39.
func HandleListResourceProviders(w http.ResponseWriter, r *http.Request) {
	log := logf.FromContext(r.Context())
	log.Info("placement request", "method", r.Method, "path", r.URL.Path)
}

// HandleCreateResourceProvider handles POST /resource_providers requests.
//
// Creates a new resource provider. The request must include a name and may
// optionally specify a UUID and a parent_provider_uuid (since 1.14) to place
// the provider in a hierarchical tree. If no UUID is supplied, one is
// generated. Before microversion 1.37, the parent of a resource provider
// could not be changed after creation.
//
// The response changed at microversion 1.20: earlier versions return only
// an HTTP 201 with a Location header, while 1.20+ returns the full resource
// provider object in the body. Returns 409 Conflict if a provider with the
// same name or UUID already exists.
func HandleCreateResourceProvider(w http.ResponseWriter, r *http.Request) {
	log := logf.FromContext(r.Context())
	log.Info("placement request", "method", r.Method, "path", r.URL.Path)
}

// HandleShowResourceProvider handles GET /resource_providers/{uuid} requests.
//
// Returns a single resource provider identified by its UUID. The response
// includes the provider's name, generation (used for concurrency control in
// subsequent updates), and links. Starting at microversion 1.14, the response
// also includes parent_provider_uuid and root_provider_uuid to describe the
// provider's position in a hierarchical tree. Returns 404 if the provider
// does not exist.
func HandleShowResourceProvider(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	log := logf.FromContext(r.Context())
	log.Info("placement request", "method", r.Method, "path", r.URL.Path, "uuid", uuid)
}

// HandleUpdateResourceProvider handles PUT /resource_providers/{uuid} requests.
//
// Updates a resource provider's name and, starting at microversion 1.14, its
// parent_provider_uuid. Since microversion 1.37, the parent may be changed to
// any existing provider UUID that would not create a loop in the tree, or set
// to null to make the provider a root. Returns 409 Conflict if another
// provider already has the requested name.
func HandleUpdateResourceProvider(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	log := logf.FromContext(r.Context())
	log.Info("placement request", "method", r.Method, "path", r.URL.Path, "uuid", uuid)
}

// HandleDeleteResourceProvider handles DELETE /resource_providers/{uuid} requests.
//
// Deletes a resource provider and disassociates all its aggregates and
// inventories. The operation fails with 409 Conflict if there are any
// allocations against the provider's inventories or if the provider has
// child providers in a tree hierarchy. Returns 204 No Content on success.
func HandleDeleteResourceProvider(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	log := logf.FromContext(r.Context())
	log.Info("placement request", "method", r.Method, "path", r.URL.Path, "uuid", uuid)
}
