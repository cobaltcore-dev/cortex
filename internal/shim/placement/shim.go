// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"net/http"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// IndexHypervisorByID is the field index key for looking up Hypervisor
// objects by their OpenStack hypervisor ID (status.hypervisorId).
const IndexHypervisorByID = ".status.hypervisorId"

// Shim is the placement API shim. It holds a controller-runtime client for
// making Kubernetes API calls and exposes HTTP handlers that mirror the
// OpenStack Placement API surface.
type Shim struct {
	client.Client
}

// SetupWithManager registers field indexes on the manager's cache so that
// subsequent list calls are served from the informer cache rather than
// hitting the API server. This must be called before the manager is started.
//
// Calling IndexField internally invokes GetInformer, which creates and
// registers a shared informer for the indexed type (hv1.Hypervisor) with the
// cache. The informer is started later when mgr.Start() is called. This
// means no separate controller or empty Reconcile loop is needed — the
// index registration alone is sufficient to warm the cache.
func (s *Shim) SetupWithManager(mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&hv1.Hypervisor{},
		IndexHypervisorByID,
		func(obj client.Object) []string {
			h, ok := obj.(*hv1.Hypervisor)
			if !ok {
				return nil
			}
			if h.Status.HypervisorID == "" {
				return nil
			}
			return []string{h.Status.HypervisorID}
		},
	)
}

// RegisterRoutes binds all Placement API handlers to the given mux. The
// route patterns use the Go 1.22+ ServeMux syntax with explicit HTTP methods
// and path wildcards. The routes mirror the OpenStack Placement API surface
// as documented at https://docs.openstack.org/api-ref/placement/.
func (s *Shim) RegisterRoutes(mux *http.ServeMux) {
	// Root
	mux.HandleFunc("GET /{$}", s.HandleGetRoot)

	// Resource providers
	mux.HandleFunc("GET /resource_providers", s.HandleListResourceProviders)
	mux.HandleFunc("POST /resource_providers", s.HandleCreateResourceProvider)
	mux.HandleFunc("GET /resource_providers/{uuid}", s.HandleShowResourceProvider)
	mux.HandleFunc("PUT /resource_providers/{uuid}", s.HandleUpdateResourceProvider)
	mux.HandleFunc("DELETE /resource_providers/{uuid}", s.HandleDeleteResourceProvider)

	// Resource classes
	mux.HandleFunc("GET /resource_classes", s.HandleListResourceClasses)
	mux.HandleFunc("POST /resource_classes", s.HandleCreateResourceClass)
	mux.HandleFunc("GET /resource_classes/{name}", s.HandleShowResourceClass)
	mux.HandleFunc("PUT /resource_classes/{name}", s.HandleUpdateResourceClass)
	mux.HandleFunc("DELETE /resource_classes/{name}", s.HandleDeleteResourceClass)

	// Resource provider inventories
	mux.HandleFunc("GET /resource_providers/{uuid}/inventories", s.HandleListResourceProviderInventories)
	mux.HandleFunc("PUT /resource_providers/{uuid}/inventories", s.HandleUpdateResourceProviderInventories)
	mux.HandleFunc("DELETE /resource_providers/{uuid}/inventories", s.HandleDeleteResourceProviderInventories)
	mux.HandleFunc("GET /resource_providers/{uuid}/inventories/{resource_class}", s.HandleShowResourceProviderInventory)
	mux.HandleFunc("PUT /resource_providers/{uuid}/inventories/{resource_class}", s.HandleUpdateResourceProviderInventory)
	mux.HandleFunc("DELETE /resource_providers/{uuid}/inventories/{resource_class}", s.HandleDeleteResourceProviderInventory)

	// Resource provider aggregates
	mux.HandleFunc("GET /resource_providers/{uuid}/aggregates", s.HandleListResourceProviderAggregates)
	mux.HandleFunc("PUT /resource_providers/{uuid}/aggregates", s.HandleUpdateResourceProviderAggregates)

	// Traits
	mux.HandleFunc("GET /traits", s.HandleListTraits)
	mux.HandleFunc("GET /traits/{name}", s.HandleShowTrait)
	mux.HandleFunc("PUT /traits/{name}", s.HandleUpdateTrait)
	mux.HandleFunc("DELETE /traits/{name}", s.HandleDeleteTrait)

	// Resource provider traits
	mux.HandleFunc("GET /resource_providers/{uuid}/traits", s.HandleListResourceProviderTraits)
	mux.HandleFunc("PUT /resource_providers/{uuid}/traits", s.HandleUpdateResourceProviderTraits)
	mux.HandleFunc("DELETE /resource_providers/{uuid}/traits", s.HandleDeleteResourceProviderTraits)

	// Allocations
	mux.HandleFunc("POST /allocations", s.HandleManageAllocations)
	mux.HandleFunc("GET /allocations/{consumer_uuid}", s.HandleListAllocations)
	mux.HandleFunc("PUT /allocations/{consumer_uuid}", s.HandleUpdateAllocations)
	mux.HandleFunc("DELETE /allocations/{consumer_uuid}", s.HandleDeleteAllocations)

	// Resource provider allocations
	mux.HandleFunc("GET /resource_providers/{uuid}/allocations", s.HandleListResourceProviderAllocations)

	// Usages
	mux.HandleFunc("GET /usages", s.HandleListUsages)

	// Resource provider usages
	mux.HandleFunc("GET /resource_providers/{uuid}/usages", s.HandleListResourceProviderUsages)

	// Allocation candidates
	mux.HandleFunc("GET /allocation_candidates", s.HandleListAllocationCandidates)

	// Reshaper
	mux.HandleFunc("POST /reshaper", s.HandlePostReshaper)
}
