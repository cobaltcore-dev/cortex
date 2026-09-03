// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	"github.com/cobaltcore-dev/cortex/pkg/sso"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/resource"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

var (
	// setupLog is a controller-runtime logger used for setup and route
	// registration. Individual handlers should use their own loggers derived
	// from the request context.
	setupLog = ctrl.Log.WithName("placement-shim")
)

// contextKey is an unexported type for context keys in this package.
type contextKey struct{}

// routePatternKey is the context key used to pass the route pattern from the
// measurement middleware (set in RegisterRoutes) to the forward method.
var routePatternKey = contextKey{}

// requestIDContextKey is a separate type so it cannot collide with routePatternKey.
type requestIDContextKey struct{}

// requestIDKey is the context key used to propagate the X-OpenStack-Request-Id
// header value through the request lifecycle for tracing.
var requestIDKey = requestIDContextKey{}

// featuresConfig toggles the KVM-backend behavior for each endpoint group.
// Every field defaults to false (pure passthrough) when omitted.
type featuresConfig struct {
	ResourceProviders      bool `json:"resourceProviders,omitempty"`
	Root                   bool `json:"root,omitempty"`
	Traits                 bool `json:"traits,omitempty"`
	ResourceProviderTraits bool `json:"resourceProviderTraits,omitempty"`
	ResourceClasses        bool `json:"resourceClasses,omitempty"`
	Inventories            bool `json:"inventories,omitempty"`
	Aggregates             bool `json:"aggregates,omitempty"`
	Allocations            bool `json:"allocations,omitempty"`
	Usages                 bool `json:"usages,omitempty"`
	AllocationCandidates   bool `json:"allocationCandidates,omitempty"`
	Reshaper               bool `json:"reshaper,omitempty"`
}

// config holds configuration for the placement shim.
type config struct {
	// SSO is an optional configuration for the certificates the http client
	// should use when talking to the placement API over ingress with single-sign-on.
	SSO *sso.SSOConfig `json:"sso,omitempty"`
	// PlacementURL is the URL of the OpenStack Placement API the shim
	// should forward requests to.
	PlacementURL string `json:"placementURL,omitempty"`
	// KeystoneURL is the URL of the OpenStack Keystone identity service
	// used for token introspection by the auth middleware and for E2E
	// test authentication.
	KeystoneURL string `json:"keystoneURL,omitempty"`
	// OSUsername is the OpenStack username for Keystone authentication
	// (OS_USERNAME). Required when auth is configured.
	OSUsername string `json:"osUsername,omitempty"`
	// OSPassword is the OpenStack password for Keystone authentication
	// (OS_PASSWORD). Required when auth is configured.
	OSPassword string `json:"osPassword,omitempty"`
	// OSProjectName is the OpenStack project name for Keystone
	// authentication (OS_PROJECT_NAME). Required when auth is configured.
	OSProjectName string `json:"osProjectName,omitempty"`
	// OSUserDomainName is the OpenStack user domain name for Keystone
	// authentication (OS_USER_DOMAIN_NAME). Required when auth is
	// configured.
	OSUserDomainName string `json:"osUserDomainName,omitempty"`
	// OSProjectDomainName is the OpenStack project domain name for
	// Keystone authentication (OS_PROJECT_DOMAIN_NAME). Required when
	// auth is configured.
	OSProjectDomainName string `json:"osProjectDomainName,omitempty"`
	// Auth configures Keystone token validation. When nil, auth is
	// disabled and requests pass through without access checks.
	Auth *authConfig `json:"auth,omitempty"`
	// MaxBodyLogSize is the maximum number of bytes of request/response
	// bodies to include in debug-level log lines, specified as a
	// Kubernetes resource.Quantity string (e.g. "4Ki"). Defaults to "4Ki"
	// when unset or empty.
	MaxBodyLogSize string `json:"maxBodyLogSize,omitempty"`
	// Features toggles the KVM-backend behavior for each endpoint group.
	// Every field defaults to false (pure passthrough) when omitted.
	Features featuresConfig `json:"features"`
}

// validate checks the config for required fields and returns an error if the
// config is invalid.
func (c *config) validate() error {
	if c.PlacementURL == "" {
		return errors.New("placement URL is required")
	}
	if c.Auth != nil && c.KeystoneURL == "" {
		return errors.New("keystoneURL is required when auth is configured")
	}
	if c.Auth != nil && c.OSUsername == "" {
		return errors.New("osUsername is required when auth is configured")
	}
	if c.Auth != nil && c.OSPassword == "" {
		return errors.New("osPassword is required when auth is configured")
	}
	if c.Auth != nil && len(c.Auth.Policies) == 0 {
		return errors.New("auth.policies must not be empty when auth is configured")
	}
	return nil
}

// Shim is the placement API shim. It holds a controller-runtime client for
// making Kubernetes API calls and exposes HTTP handlers that mirror the
// OpenStack Placement API surface.
//
// The Shim is process-lifetime: it is built and initialized once (Init) and its
// HTTP routes are registered once (RegisterRoutes). The controller/cache it
// watches, however, may be rebuilt underneath it by a self-healing supervisor
// (see pkg/shim/supervisor), which calls SetupControllerWithManager again for
// each fresh manager. The HTTP request path is pure passthrough today and does
// not touch the cache, so it is unaffected by manager restarts. Handlers that
// do need the cache can gate on ManagerReady, which the supervisor drives via
// SetManagerReady (true once the cache is synced, false when the manager is
// down or restarting).
type Shim struct {
	client.Client
	config config
	// managerReady is true while a controller-manager is running with a synced
	// cache. It is driven by the self-healing supervisor via SetManagerReady and
	// read on the request path via ManagerReady so cache-backed handlers can
	// return 503 while the cache is unavailable. Passthrough handlers ignore it.
	// It is a pointer to the atomic holder (rather than an embedded atomic value)
	// so the Shim struct itself stays copy-safe for tests.
	managerReady *atomic.Bool
	// HTTP client that can talk to openstack placement, if needed, over
	// ingress with single-sign-on.
	httpClient *http.Client
	// maxBodyLogSize is the maximum number of bytes of request/response
	// bodies to capture for debug-level logging. Parsed from
	// config.MaxBodyLogSize at setup time.
	maxBodyLogSize int64

	// downstreamRequestTimer is a prometheus histogram to measure the duration
	// (and count) of requests coming from the client that wants to talk to the
	// placement API.
	downstreamRequestTimer *prometheus.HistogramVec
	// upstreamRequestTimer is a prometheus histogram to measure the duration
	// (and count) of requests to the upstream placement API by route and method.
	upstreamRequestTimer *prometheus.HistogramVec

	// authPolicies is the pre-compiled policy table. Nil when auth is
	// disabled (config.Auth is nil).
	authPolicies []compiledPolicy
	// tokenCache caches validated token info to avoid repeated Keystone
	// introspection.
	tokenCache *tokenCache
	// tokenIntrospector validates tokens against Keystone.
	tokenIntrospector tokenIntrospector
}

// Describe implements prometheus.Collector.
func (s *Shim) Describe(ch chan<- *prometheus.Desc) {
	s.downstreamRequestTimer.Describe(ch)
	s.upstreamRequestTimer.Describe(ch)
}

// Collect implements prometheus.Collector.
func (s *Shim) Collect(ch chan<- prometheus.Metric) {
	s.downstreamRequestTimer.Collect(ch)
	s.upstreamRequestTimer.Collect(ch)
}

// initHTTPClient builds the HTTP transport (with optional SSO TLS) and
// verifies connectivity to the upstream placement API. Called during Start.
func (s *Shim) initHTTPClient(ctx context.Context) error {
	var transport *http.Transport
	var err error
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
	transport.DialContext = (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = 60 * time.Second
	transport.ExpectContinueTimeout = 1 * time.Second
	transport.IdleConnTimeout = 90 * time.Second
	s.httpClient = &http.Client{Transport: sso.WrapUserAgent(transport), Timeout: 60 * time.Second}

	setupLog.Info("Testing connection to placement API", "url", s.config.PlacementURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.config.PlacementURL, http.NoBody)
	if err != nil {
		setupLog.Error(err, "Failed to create HTTP request to placement API")
		return err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		setupLog.Info("WARNING: Failed to connect to placement API at startup, continuing anyway", "error", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		setupLog.Info("WARNING: Unexpected response from placement API at startup, continuing anyway", "status", resp.Status)
		return nil
	}
	setupLog.Info("Successfully connected to placement API")
	return nil
}

// SetManagerReady records whether a controller-manager with a synced cache is
// currently running. The self-healing supervisor sets it true once the cache
// has synced and false when the manager cycle ends (see pkg/shim/supervisor and
// cmd/shim).
//
// Once Init has run, the holder is allocated and calls to SetManagerReady and
// ManagerReady are race-free (they only load/store the atomic). The nil guard
// below is a convenience for shims constructed in tests without Init; in that
// case the first SetManagerReady is NOT safe to race with a concurrent
// ManagerReady, since it may write the s.managerReady pointer itself. Production
// code always goes through Init before serving, so this is not a concern there.
func (s *Shim) SetManagerReady(ready bool) {
	if s.managerReady == nil {
		s.managerReady = &atomic.Bool{}
	}
	s.managerReady.Store(ready)
}

// ManagerReady reports whether a controller-manager with a synced cache is
// currently running. Cache-backed handlers should gate on it and return 503
// when it is false; passthrough handlers, which never touch the cache, ignore
// it. It defaults to false until the supervisor brings the first manager up.
// Safe to call concurrently once Init has allocated the holder (see
// SetManagerReady).
func (s *Shim) ManagerReady() bool {
	return s.managerReady != nil && s.managerReady.Load()
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

// Init performs the once-only, manager-independent setup of the shim: it loads
// and validates the shim config, compiles the auth policies, allocates the
// Prometheus metric vectors, and initializes the upstream HTTP client and
// Keystone token introspector. It must be called exactly once per process
// (before RegisterRoutes and before the metric collectors are registered),
// because it allocates collectors and the HTTP request path depends on the
// fields it sets. It is intentionally decoupled from the controller-manager
// lifecycle so that the HTTP layer survives manager restarts (see
// pkg/shim/supervisor).
func (s *Shim) Init(ctx context.Context) (err error) {
	setupLog.Info("Initializing placement shim")

	// Allocate the readiness holder before any HTTP handler can run, so the
	// pointer itself is never written concurrently with ManagerReady reads from
	// the request path (the API server comes up before the first cache sync).
	if s.managerReady == nil {
		s.managerReady = &atomic.Bool{}
	}

	s.config, err = conf.GetConfig[config]()
	if err != nil {
		setupLog.Error(err, "Failed to load placement shim config")
		return err
	}
	if err := s.config.validate(); err != nil {
		return err
	}

	// Parse the body log size limit from config (default 4Ki).
	bodyLogQty := s.config.MaxBodyLogSize
	if bodyLogQty == "" {
		bodyLogQty = "4Ki"
	}
	qty, err := resource.ParseQuantity(bodyLogQty)
	if err != nil {
		return fmt.Errorf("invalid maxBodyLogSize %q: %w", bodyLogQty, err)
	}
	s.maxBodyLogSize = qty.Value()

	if err := s.compileAuthPolicies(); err != nil {
		return err
	}

	// Initialize Prometheus histogram timers for request monitoring.
	s.downstreamRequestTimer = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_placement_shim_downstream_request_duration_seconds",
		Help:    "Duration of downstream requests to the placement shim from clients.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "pattern", "responsecode"})
	s.upstreamRequestTimer = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_placement_shim_upstream_request_duration_seconds",
		Help:    "Duration of upstream requests from the placement shim to the placement API.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "pattern", "responsecode"})

	// Initialize the upstream HTTP client and token introspector. These are
	// safe to set up before any manager exists and do not depend on the cache.
	if err := s.initHTTPClient(ctx); err != nil {
		return err
	}
	if err := s.initTokenIntrospector(ctx); err != nil {
		return err
	}
	return nil
}

// SetupControllerWithManager wires the shim's Hypervisor watch into the given
// manager, backed by the given multicluster client. It sets up the field
// indexes and registers a watch across all clusters serving the Hypervisor
// GVK. Unlike Init, this is called once per manager cycle: the self-healing
// supervisor rebuilds the manager (and its caches) on connectivity failure and
// calls this again for the fresh manager. It does NOT touch the HTTP layer,
// metric collectors, or config.
func (s *Shim) SetupControllerWithManager(ctx context.Context, mgr ctrl.Manager, mcl *multicluster.Client) error {
	setupLog.Info("Setting up placement shim controller with manager")
	if mcl == nil {
		return errors.New("multicluster client must not be nil")
	}
	// Store the fresh multicluster client as the shim's cache-backed client so
	// cache-read handlers have a live client for this manager cycle. It is
	// re-assigned on every rebuild; readiness is gated separately via
	// SetManagerReady once the cache has synced.
	s.Client = mcl
	if err := IndexFields(ctx, mcl); err != nil {
		return fmt.Errorf("failed to set up indexes: %w", err)
	}
	bldr := multicluster.BuildController(mcl, mgr)
	bldr, err := bldr.WatchesMulticluster(&hv1.Hypervisor{},
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
// The route pattern for metric labels is read from the request context
// (set by the measurement middleware in RegisterRoutes).
func (s *Shim) forward(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)
	log.Info("Forwarding request to placement API",
		"method", r.Method, "path", r.URL.Path)

	if s.httpClient == nil {
		log.Info("placement shim not yet initialized, rejecting request")
		http.Error(w, "service not ready", http.StatusServiceUnavailable)
		return
	}

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
	url := upstream.String()
	log.Info("Calling URL", "url", url)
	upstreamReq, err := http.NewRequestWithContext(ctx, r.Method, url, r.Body)
	if err != nil {
		log.Error(err, "failed to create upstream request", "url", url)
		http.Error(w, "failed to create upstream request", http.StatusBadGateway)
		return
	}

	// Copy all incoming headers.
	upstreamReq.Header = r.Header.Clone()

	pattern, _ := ctx.Value(routePatternKey).(string)
	start := time.Now()
	resp, err := s.httpClient.Do(upstreamReq) //nolint:gosec // G704: intentional reverse proxy
	if err != nil {
		log.Error(err, "failed to reach placement API", "url", url)
		s.upstreamRequestTimer.
			WithLabelValues(r.Method, pattern, strconv.Itoa(http.StatusBadGateway)).
			Observe(time.Since(start).Seconds())
		http.Error(w, "failed to reach placement API", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Observe after the response is received (the copy below consumes the
	// body, but the upstream latency is already known).
	s.upstreamRequestTimer.
		WithLabelValues(r.Method, pattern, strconv.Itoa(resp.StatusCode)).
		Observe(time.Since(start).Seconds())

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
		mux.HandleFunc(fmt.Sprintf("%s %s", h.method, h.pattern), s.wrapHandler(h.pattern, h.handler))
	}
	setupLog.Info("Successfully registered placement API routes")
}
