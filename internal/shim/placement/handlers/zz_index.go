// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package handlers

import "net/http"

// RegisterRoutes binds all Placement API handlers to the given mux. The
// route patterns use the Go 1.22+ ServeMux syntax with explicit HTTP methods
// and path wildcards. The routes mirror the OpenStack Placement API surface
// as documented at https://docs.openstack.org/api-ref/placement/.
func RegisterRoutes(mux *http.ServeMux) {
	// Root
	mux.HandleFunc("GET /{$}", HandleGetRoot)

	// Resource providers
	mux.HandleFunc("GET /resource_providers", HandleListResourceProviders)
	mux.HandleFunc("POST /resource_providers", HandleCreateResourceProvider)
	mux.HandleFunc("GET /resource_providers/{uuid}", HandleShowResourceProvider)
	mux.HandleFunc("PUT /resource_providers/{uuid}", HandleUpdateResourceProvider)
	mux.HandleFunc("DELETE /resource_providers/{uuid}", HandleDeleteResourceProvider)

	// Resource classes
	mux.HandleFunc("GET /resource_classes", HandleListResourceClasses)
	mux.HandleFunc("POST /resource_classes", HandleCreateResourceClass)
	mux.HandleFunc("GET /resource_classes/{name}", HandleShowResourceClass)
	mux.HandleFunc("PUT /resource_classes/{name}", HandleUpdateResourceClass)
	mux.HandleFunc("DELETE /resource_classes/{name}", HandleDeleteResourceClass)

	// Resource provider inventories
	mux.HandleFunc("GET /resource_providers/{uuid}/inventories", HandleListResourceProviderInventories)
	mux.HandleFunc("PUT /resource_providers/{uuid}/inventories", HandleUpdateResourceProviderInventories)
	mux.HandleFunc("DELETE /resource_providers/{uuid}/inventories", HandleDeleteResourceProviderInventories)
	mux.HandleFunc("GET /resource_providers/{uuid}/inventories/{resource_class}", HandleShowResourceProviderInventory)
	mux.HandleFunc("PUT /resource_providers/{uuid}/inventories/{resource_class}", HandleUpdateResourceProviderInventory)
	mux.HandleFunc("DELETE /resource_providers/{uuid}/inventories/{resource_class}", HandleDeleteResourceProviderInventory)

	// Resource provider aggregates
	mux.HandleFunc("GET /resource_providers/{uuid}/aggregates", HandleListResourceProviderAggregates)
	mux.HandleFunc("PUT /resource_providers/{uuid}/aggregates", HandleUpdateResourceProviderAggregates)

	// Traits
	mux.HandleFunc("GET /traits", HandleListTraits)
	mux.HandleFunc("GET /traits/{name}", HandleShowTrait)
	mux.HandleFunc("PUT /traits/{name}", HandleUpdateTrait)
	mux.HandleFunc("DELETE /traits/{name}", HandleDeleteTrait)

	// Resource provider traits
	mux.HandleFunc("GET /resource_providers/{uuid}/traits", HandleListResourceProviderTraits)
	mux.HandleFunc("PUT /resource_providers/{uuid}/traits", HandleUpdateResourceProviderTraits)
	mux.HandleFunc("DELETE /resource_providers/{uuid}/traits", HandleDeleteResourceProviderTraits)

	// Allocations
	mux.HandleFunc("POST /allocations", HandleManageAllocations)
	mux.HandleFunc("GET /allocations/{consumer_uuid}", HandleListAllocations)
	mux.HandleFunc("PUT /allocations/{consumer_uuid}", HandleUpdateAllocations)
	mux.HandleFunc("DELETE /allocations/{consumer_uuid}", HandleDeleteAllocations)

	// Resource provider allocations
	mux.HandleFunc("GET /resource_providers/{uuid}/allocations", HandleListResourceProviderAllocations)

	// Usages
	mux.HandleFunc("GET /usages", HandleListUsages)

	// Resource provider usages
	mux.HandleFunc("GET /resource_providers/{uuid}/usages", HandleListResourceProviderUsages)

	// Allocation candidates
	mux.HandleFunc("GET /allocation_candidates", HandleListAllocationCandidates)

	// Reshaper
	mux.HandleFunc("POST /reshaper", HandlePostReshaper)
}
