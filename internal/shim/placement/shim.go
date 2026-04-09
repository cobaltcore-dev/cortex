// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"errors"
	"net/http"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/sso"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// IndexHypervisorByID is the field index key for looking up Hypervisor
// objects by their OpenStack hypervisor ID (status.hypervisorId).
const IndexHypervisorByID = ".status.hypervisorId"

var (
	// setupLog is a controller-runtime logger used for setup and route
	// registration. Individual handlers should use their own loggers derived
	// from the request context.
	setupLog = ctrl.Log.WithName("placement-shim")
)

// config holds configuration for the placement shim.
type config struct {
	// SSO is an optional reference to a Kubernetes secret containing
	// credentials to talk to openstack over ingress via single-sign-on.
	SSO *sso.SSOConfig `json:"sso,omitempty"`
	// PlacementURL is the URL of the OpenStack Placement API the shim
	// should forward requests to.
	PlacementURL string `json:"placementURL,omitempty"`
}

// Shim is the placement API shim. It holds a controller-runtime client for
// making Kubernetes API calls and exposes HTTP handlers that mirror the
// OpenStack Placement API surface.
type Shim struct {
	client.Client
	config config
	// HTTP client that can talk to openstack placement, if needed, over
	// ingress with single-sign-on.
	httpClient *http.Client
}

// Start is called after the manager has started and the cache is running.
// It can be used to perform any initialization that requires the cache to be
// running.
func (s *Shim) Start(ctx context.Context) (err error) {
	setupLog.Info("Starting placement shim")
	s.httpClient = http.DefaultClient
	if s.config.SSO != nil {
		setupLog.Info("SSO config provided, creating HTTP client for placement API")
		s.httpClient, err = sso.NewHTTPClient(*s.config.SSO)
		if err != nil {
			setupLog.Error(err, "Failed to create HTTP client from SSO config")
			return err
		}
		setupLog.Info("Successfully created HTTP client from SSO config")
	} else {
		setupLog.Info("No SSO config provided, using default HTTP client for placement API")
	}
	// Try establish a connection to the placement API to fail fast if the
	// configuration is invalid. Directly call the root endpoint for that.
	setupLog.Info("Testing connection to placement API", "url", s.config.PlacementURL)
	if s.config.PlacementURL == "" {
		err := errors.New("placement URL is not configured")
		setupLog.Error(err, "Invalid configuration for placement shim")
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", s.config.PlacementURL, nil)
	if err != nil {
		setupLog.Error(err, "Failed to create HTTP request to placement API")
		return err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		setupLog.Error(err, "Failed to connect to placement API")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		err := errors.New("unexpected response from placement API")
		setupLog.Error(err, "Failed to call placement API", "status", resp.Status)
		return err
	}
	setupLog.Info("Successfully connected to placement API")
	return nil
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
func (s *Shim) SetupWithManager(ctx context.Context, mgr ctrl.Manager) (err error) {
	setupLog.Info("Setting up placement shim with manager")
	if err := mgr.Add(s); err != nil { // Bind Start(ctx)
		setupLog.Error(err, "Failed to bind start routine")
		return err
	}
	s.config, err = conf.GetConfig[config]()
	if err != nil {
		setupLog.Error(err, "Failed to load placement shim config")
		return err
	}
	setupLog.Info("Indexing Hypervisors by hypervisor ID")
	err = mgr.GetFieldIndexer().IndexField(ctx, &hv1.Hypervisor{}, IndexHypervisorByID,
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
	if err != nil {
		setupLog.Error(err, "Failed to index Hypervisors by hypervisor ID")
		return err
	}
	setupLog.Info("Successfully indexed Hypervisors by hypervisor ID")
	return nil
}

// RegisterRoutes binds all Placement API handlers to the given mux. The
// route patterns use the Go 1.22+ ServeMux syntax with explicit HTTP methods
// and path wildcards. The routes mirror the OpenStack Placement API surface
// as documented at https://docs.openstack.org/api-ref/placement/.
func (s *Shim) RegisterRoutes(mux *http.ServeMux) {
	setupLog.Info("Registering placement API routes")
	handlers := []struct {
		method  string
		pattern string
		handler http.HandlerFunc
	}{
		{"GET", "/{$}", s.HandleGetRoot},
		{"GET", "/resource_providers", s.HandleListResourceProviders},
		{"POST", "/resource_providers", s.HandleCreateResourceProvider},
		{"GET", "/resource_providers/{uuid}", s.HandleShowResourceProvider},
		{"PUT", "/resource_providers/{uuid}", s.HandleUpdateResourceProvider},
		{"DELETE", "/resource_providers/{uuid}", s.HandleDeleteResourceProvider},
		{"GET", "/resource_classes", s.HandleListResourceClasses},
		{"POST", "/resource_classes", s.HandleCreateResourceClass},
		{"GET", "/resource_classes/{name}", s.HandleShowResourceClass},
		{"PUT", "/resource_classes/{name}", s.HandleUpdateResourceClass},
		{"DELETE", "/resource_classes/{name}", s.HandleDeleteResourceClass},
		{"GET", "/resource_providers/{uuid}/inventories", s.HandleListResourceProviderInventories},
		{"PUT", "/resource_providers/{uuid}/inventories", s.HandleUpdateResourceProviderInventories},
		{"DELETE", "/resource_providers/{uuid}/inventories", s.HandleDeleteResourceProviderInventories},
		{"GET", "/resource_providers/{uuid}/inventories/{resource_class}", s.HandleShowResourceProviderInventory},
		{"PUT", "/resource_providers/{uuid}/inventories/{resource_class}", s.HandleUpdateResourceProviderInventory},
		{"DELETE", "/resource_providers/{uuid}/inventories/{resource_class}", s.HandleDeleteResourceProviderInventory},
		{"GET", "/resource_providers/{uuid}/aggregates", s.HandleListResourceProviderAggregates},
		{"PUT", "/resource_providers/{uuid}/aggregates", s.HandleUpdateResourceProviderAggregates},
		{"GET", "/traits", s.HandleListTraits},
		{"GET", "/traits/{name}", s.HandleShowTrait},
		{"PUT", "/traits/{name}", s.HandleUpdateTrait},
		{"DELETE", "/traits/{name}", s.HandleDeleteTrait},
		{"GET", "/resource_providers/{uuid}/traits", s.HandleListResourceProviderTraits},
		{"PUT", "/resource_providers/{uuid}/traits", s.HandleUpdateResourceProviderTraits},
		{"DELETE", "/resource_providers/{uuid}/traits", s.HandleDeleteResourceProviderTraits},
		{"POST", "/allocations", s.HandleManageAllocations},
		{"GET", "/allocations/{consumer_uuid}", s.HandleListAllocations},
		{"PUT", "/allocations/{consumer_uuid}", s.HandleUpdateAllocations},
		{"DELETE", "/allocations/{consumer_uuid}", s.HandleDeleteAllocations},
		{"GET", "/resource_providers/{uuid}/allocations", s.HandleListResourceProviderAllocations},
		{"GET", "/usages", s.HandleListUsages},
		{"GET", "/resource_providers/{uuid}/usages", s.HandleListResourceProviderUsages},
		{"GET", "/allocation_candidates", s.HandleListAllocationCandidates},
		{"POST", "/reshaper", s.HandlePostReshaper},
	}
	for _, h := range handlers {
		setupLog.Info("Registering route", "method", h.method, "pattern", h.pattern)
		mux.HandleFunc(h.method+" "+h.pattern, h.handler)
	}
	setupLog.Info("Successfully registered placement API routes")
}
