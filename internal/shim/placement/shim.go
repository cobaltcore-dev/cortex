// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	"github.com/cobaltcore-dev/cortex/pkg/sso"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
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

// validate checks the config for required fields and returns an error if the
// config is invalid.
func (c *config) validate() error {
	if c.PlacementURL == "" {
		return errors.New("placement URL is required")
	}
	return nil
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
	// Build the transport with optional SSO TLS credentials.
	var transport *http.Transport
	if s.config.SSO != nil {
		setupLog.Info("SSO config provided, creating transport for placement API")
		transport, err = sso.NewTransport(*s.config.SSO)
		if err != nil {
			setupLog.Error(err, "Failed to create transport from SSO config")
			return err
		}
	} else {
		setupLog.Info("No SSO config provided, using plain transport for placement API")
		transport = &http.Transport{}
	}
	// All proxy traffic goes to one placement API host, so raise the
	// per-host idle connection limit from the default of 2.
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 100
	// Guard against a hung upstream or slow TLS negotiation.
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = 60 * time.Second
	transport.ExpectContinueTimeout = 1 * time.Second
	transport.IdleConnTimeout = 90 * time.Second
	s.httpClient = &http.Client{Transport: transport}
	// Try establish a connection to the placement API to fail fast if the
	// configuration is invalid. Directly call the root endpoint for that.
	setupLog.Info("Testing connection to placement API", "url", s.config.PlacementURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.config.PlacementURL, http.NoBody)
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

// Reconcile is not used by the shim, but must be implemented to satisfy the
// controller-runtime Reconciler interface.
func (s *Shim) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

// handleRemoteHypervisor is called by watches in remote clusters and triggers
// a reconcile on the hypervisor resource that was changed in the remote cluster.
func (s *Shim) handleRemoteHypervisor() handler.EventHandler {
	handler := handler.Funcs{}
	// For now, the shim doesn't need to do anything on hypervisor events.
	return handler
}

// predicateRemoteHypervisor is used to filter events from remote clusters,
// so that only events for hypervisors that should be processed by the shim.
func (s *Shim) predicateRemoteHypervisor() predicate.Predicate {
	// For now, the shim doesn't need to process any hypervisor events.
	return predicate.NewPredicateFuncs(func(object client.Object) bool {
		return false
	})
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
	s.config, err = conf.GetConfig[config]()
	if err != nil {
		setupLog.Error(err, "Failed to load placement shim config")
		return err
	}
	// Validate we don't have any weird values in the config.
	if err := s.config.validate(); err != nil {
		return err
	}
	// Check that the provided client is a multicluster client, since we need
	// that to watch for hypervisors across clusters.
	mcl, ok := s.Client.(*multicluster.Client)
	if !ok {
		return errors.New("provided client must be a multicluster client")
	}
	bldr := multicluster.BuildController(mcl, mgr)
	// The hypervisor crd may be distributed across multiple remote clusters.
	bldr, err = bldr.WatchesMulticluster(&hv1.Hypervisor{},
		s.handleRemoteHypervisor(),
		s.predicateRemoteHypervisor(),
	)
	if err != nil {
		return err
	}
	return bldr.Named("placement-shim").Complete(s)
}

// forward proxies the incoming HTTP request to the upstream placement API
// and copies the response (status, headers, body) back to the client.
func (s *Shim) forward(w http.ResponseWriter, r *http.Request) {
	log := logf.FromContext(r.Context())

	// Parse the trusted base URL and resolve the request path against it
	// so the upstream target is always anchored to the configured host.
	upstream, err := url.Parse(s.config.PlacementURL)
	if err != nil {
		log.Error(err, "failed to parse placement URL", "url", s.config.PlacementURL)
		http.Error(w, "failed to parse placement URL", http.StatusBadGateway)
		return
	}
	upstream.Path, err = url.JoinPath(upstream.Path, r.URL.Path)
	if err != nil {
		log.Error(err, "failed to join upstream path", "path", r.URL.Path)
		http.Error(w, "failed to join upstream path", http.StatusBadGateway)
		return
	}
	upstream.RawQuery = r.URL.RawQuery

	// Create upstream request preserving method, body, and context.
	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstream.String(), r.Body)
	if err != nil {
		log.Error(err, "failed to create upstream request", "url", upstream.String())
		http.Error(w, "failed to create upstream request", http.StatusBadGateway)
		return
	}

	// Copy all incoming headers.
	upstreamReq.Header = r.Header.Clone()

	resp, err := s.httpClient.Do(upstreamReq) //nolint:gosec // G704: intentional reverse proxy; host is fixed by operator config, only path varies
	if err != nil {
		log.Error(err, "failed to reach placement API", "url", upstream.String())
		http.Error(w, "failed to reach placement API", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers, status code, and body back to the caller.
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Error(err, "failed to copy upstream response body")
	}
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
