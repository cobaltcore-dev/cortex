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
type Shim struct {
	client.Client
	config config
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
	s.httpClient = &http.Client{Transport: transport, Timeout: 60 * time.Second}

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

// Start is called after the manager has started and the cache is running.
func (s *Shim) Start(ctx context.Context) error {
	setupLog.Info("Starting placement shim")
	if err := s.initHTTPClient(ctx); err != nil {
		return err
	}
	return s.initTokenIntrospector(ctx)
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

// SetupWithManager sets up the controller with the manager.
// It registers watches for the Hypervisor CRD across all clusters and sets up
// the HTTP client for talking to the placement API.
func (s *Shim) SetupWithManager(ctx context.Context, mgr ctrl.Manager) (err error) {
	setupLog.Info("Setting up placement shim with manager")

	if err := mgr.Add(s); err != nil {
		return err
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

	// Check that the provided client is a multicluster client, since we need
	// that to watch for hypervisors across clusters.
	mcl, ok := s.Client.(*multicluster.Client)
	if !ok {
		return errors.New("provided client must be a multicluster client")
	}
	if err := indexFields(ctx, mcl); err != nil {
		return fmt.Errorf("failed to set up indexes: %w", err)
	}
	bldr := multicluster.BuildController(mcl, mgr)
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
// The route pattern for metric labels is read from the request context
// (set by the measurement middleware in RegisterRoutes).
func (s *Shim) forward(w http.ResponseWriter, r *http.Request) {
	s.forwardWithHook(w, r, nil)
}

// forwardWithHook works like forward but accepts an optional intercept
// callback. When hook is non-nil and the upstream returns a successful
// response, the hook receives the *http.Response and is responsible for
// writing the final response to w. If hook is nil the response is copied
// through unchanged, identical to forward.
func (s *Shim) forwardWithHook(w http.ResponseWriter, r *http.Request, hook func(w http.ResponseWriter, resp *http.Response)) {
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

	// Observe after the response is received (the hook or copy below
	// may consume the body, but the upstream latency is already known).
	s.upstreamRequestTimer.
		WithLabelValues(r.Method, pattern, strconv.Itoa(resp.StatusCode)).
		Observe(time.Since(start).Seconds())

	if hook != nil {
		hook(w, resp)
		return
	}

	// Default: copy response headers, status code, and body back to the caller.
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
