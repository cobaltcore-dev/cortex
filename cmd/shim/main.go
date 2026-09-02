// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/shim/placement"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/monitoring"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	"github.com/cobaltcore-dev/cortex/pkg/shim/supervisor"
	"github.com/cobaltcore-dev/cortex/pkg/sso"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-bits/httpext"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

var (
	// Scheme defines the scheme for the API types used by the shim.
	scheme = runtime.NewScheme()
	// setupLog is the logger used for setup operations in the shim.
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	// Bind the Kubernetes client-go scheme and the custom API types to the
	// scheme used by the shim.
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme)) // Cortex crds
	utilruntime.Must(hv1.AddToScheme(scheme))      // Hypervisor crd
}

// UserAgentConfig identifies this cortex deployment to the services it talks
// to. Rendered by helm from the release name and chart version.
type UserAgentConfig struct {
	// Component is the service name, e.g. "cortex-nova".
	Component string `json:"component,omitempty"`
	// Version is the deployed version, e.g. "sha-70af93a8".
	Version string `json:"version,omitempty"`
}

type MainConfig struct {
	// User-Agent sent with all outgoing HTTP requests.
	UserAgent UserAgentConfig `json:"userAgent,omitempty"`
}

func main() {
	ctx := ctrl.SetupSignalHandler()

	mainConfig := conf.GetConfigOrDie[MainConfig]()
	restConfig := ctrl.GetConfigOrDie()

	// Identify this cortex deployment to the services it talks to via the
	// User-Agent header, before any HTTP requests are made. The shared
	// http.DefaultTransport covers http.DefaultClient and any http.Client
	// without its own transport; SSO clients build their own transport and
	// are handled separately by pkg/sso.
	httpext.WrapTransport(&http.DefaultTransport).
		SetOverrideUserAgent(mainConfig.UserAgent.Component, mainConfig.UserAgent.Version)
	sso.SetUserAgent(mainConfig.UserAgent.Component, mainConfig.UserAgent.Version)

	var metricsAddr string
	var apiBindAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	// The shim does not require leader election, but this flag is provided to
	// stay consistent with the kubebuilder scaffold.
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var enablePlacementShim bool
	var runPlacementShimE2E bool
	var selfHeal bool
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&apiBindAddr, "api-bind-address", ":8080", "The address the shim API server binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.BoolVar(&enablePlacementShim, "placement-shim", false,
		"If set, the placement API shim handlers are registered on the API server.")
	flag.BoolVar(&runPlacementShimE2E, "e2e-placement-shim", false,
		"If set, runs end-to-end tests for the placement shim instead of starting the manager. ")
	flag.BoolVar(&selfHeal, "self-heal", true,
		"If set (the default), the controller-manager runs under a supervisor that rebuilds it "+
			"(cache and controllers) with backoff on failure, while the REST API, liveness probe, and "+
			"metrics endpoint run in the durable outer process and survive manager restarts. The pod never "+
			"crashes on apiserver/cache connectivity issues; a looping manager is surfaced via the "+
			"cortex_placement_shim_manager_up gauge instead. Set --self-heal=false to fall back to the "+
			"coupled behavior where a manager failure exits the process.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	// Check that we're really running this shim without leader election enabled.
	if enableLeaderElection {
		err := errors.New("leader election should not be enabled for the shim")
		setupLog.Error(err, "invalid configuration")
		os.Exit(1)
	}

	// Check that the metrics and API bind addresses don't overlap.
	if metricsAddr != "0" && metricsAddr == apiBindAddr {
		err := errors.New("metrics-bind-address and api-bind-address must not be the same")
		setupLog.Error(err, "invalid configuration", "metrics-bind-address", metricsAddr, "api-bind-address", apiBindAddr)
		os.Exit(1)
	}

	// In self-heal mode the metrics endpoint is served by the durable outer
	// process (plain promhttp) so it survives manager restarts, which means it
	// cannot apply controller-runtime's authn/authz FilterProvider. Secure
	// metrics are therefore incompatible with self-heal; the shim always runs
	// with --metrics-secure=false, so reject the combination rather than
	// silently serving unauthenticated metrics.
	if selfHeal && secureMetrics && metricsAddr != "0" {
		err := errors.New("--metrics-secure is not supported with --self-heal; run with --metrics-secure=false or --self-heal=false")
		setupLog.Error(err, "invalid configuration")
		os.Exit(1)
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Custom entrypoint for placement shim e2e tests.
	// Spins up a minimal manager with a multicluster client so that e2e
	// tests can access the controller-runtime cache for hypervisor lookups.
	if runPlacementShimE2E {
		mgrCtx, mgrCancel := context.WithCancel(ctx)

		mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
			Scheme:                 scheme,
			Metrics:                metricsserver.Options{BindAddress: "0"},
			HealthProbeBindAddress: "",
		})
		if err != nil {
			setupLog.Error(err, "unable to start e2e manager")
			os.Exit(1)
		}
		multiclusterClient, err := setupMulticlusterClient(mgrCtx, mgr, restConfig, multicluster.NewMonitor("cortex_"))
		if err != nil {
			setupLog.Error(err, "unable to set up e2e multicluster client")
			mgrCancel()
			os.Exit(1)
		}
		if err := placement.IndexFields(mgrCtx, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to set up e2e field indexes")
			os.Exit(1)
		}
		go func() {
			if err := mgr.Start(mgrCtx); err != nil {
				setupLog.Error(err, "e2e manager exited with error")
			}
		}()
		if !mgr.GetCache().WaitForCacheSync(ctx) {
			setupLog.Error(nil, "e2e cache sync failed")
			mgrCancel()
			os.Exit(1)
		}
		if err := placement.RunE2E(ctx, multiclusterClient); err != nil {
			setupLog.Error(err, "E2E tests failed")
			mgrCancel()
			os.Exit(1)
		}
		mgrCancel()
		os.Exit(0)
	}

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Create watchers for metrics and webhooks certificates
	var metricsCertWatcher, webhookCertWatcher *certwatcher.CertWatcher

	// Initial webhook TLS options
	webhookTLSOpts := append([]func(*tls.Config){}, tlsOpts...)

	if webhookCertPath != "" {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		var err error
		webhookCertWatcher, err = certwatcher.New(
			filepath.Join(webhookCertPath, webhookCertName),
			filepath.Join(webhookCertPath, webhookCertKey),
		)
		if err != nil {
			setupLog.Error(err, "Failed to initialize webhook certificate watcher")
			os.Exit(1)
		}

		webhookTLSOpts = append(webhookTLSOpts, func(config *tls.Config) {
			config.GetCertificate = webhookCertWatcher.GetCertificate
		})
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: webhookTLSOpts,
	})

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       append([]func(*tls.Config){}, tlsOpts...),
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if metricsCertPath != "" {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		var err error
		metricsCertWatcher, err = certwatcher.New(
			filepath.Join(metricsCertPath, metricsCertName),
			filepath.Join(metricsCertPath, metricsCertKey),
		)
		if err != nil {
			setupLog.Error(err, "Failed to initialize metrics certificate watcher")
			os.Exit(1)
		}

		metricsServerOptions.TLSOpts = append(metricsServerOptions.TLSOpts, func(config *tls.Config) {
			config.GetCertificate = metricsCertWatcher.GetCertificate
		})
	}

	// Our custom monitoring registry can add prometheus labels to all metrics.
	// This is useful to distinguish metrics from different deployments. This is
	// process-lifetime state: the registry and its collectors are wrapped and
	// registered exactly once, so they stay scrapable across manager restarts in
	// self-heal mode (MustRegister panics on a duplicate).
	metricsConfig := conf.GetConfigOrDie[monitoring.Config]()
	metrics.Registry = monitoring.WrapRegistry(metrics.Registry, metricsConfig)

	// One multicluster Monitor for the whole process. In self-heal mode the
	// manager (and its multicluster client) is rebuilt per cycle, but the
	// Monitor collector must be registered only once, so it is owned here and
	// handed to each client build.
	multiclusterMonitor := multicluster.NewMonitor("cortex_")
	metrics.Registry.MustRegister(multiclusterMonitor)

	// managerUp reports whether a controller-manager is currently running: 1
	// while a manager (and its cache) is up, 0 between restarts. It is served
	// from the outer process so a crash-looping manager can be detected via
	// alerting without ever crashing the pod. Registered once.
	managerUp := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "cortex_placement_shim_manager_up",
		Help: "1 while the placement shim controller-manager is running, 0 while it is being (re)built. " +
			"A value stuck at 0 indicates the manager cannot reach the apiserver / is crash-looping " +
			"while the shim keeps serving in passthrough mode.",
	})
	metrics.Registry.MustRegister(managerUp)

	// API endpoint. The mux and the shim's HTTP layer are process-lifetime: the
	// shim is initialized and its routes and metric collectors registered exactly
	// once. In self-heal mode the supervisor rebuilds only the manager (cache +
	// controllers) per cycle; the HTTP layer here is untouched by restarts.
	mux := http.NewServeMux()
	var placementShim *placement.Shim
	if enablePlacementShim {
		placementShim = &placement.Shim{}
		if err := placementShim.Init(ctx); err != nil {
			setupLog.Error(err, "unable to initialize placement shim")
			os.Exit(1)
		}
		metrics.Registry.MustRegister(placementShim)
		placementShim.RegisterRoutes(mux)
	}

	// The liveness probe, REST API, and metrics endpoint bind addresses. In
	// self-heal mode they are owned by the durable outer process (the
	// supervisor) so they survive manager restarts, and the manager binds none
	// of them (an empty/"0" address disables the manager-owned server). In
	// coupled mode the manager owns them, matching the classic behavior.
	managerProbeAddr := probeAddr
	managerMetricsAddr := metricsServerOptions.BindAddress
	if selfHeal {
		managerProbeAddr = ""
		metricsServerOptions.BindAddress = "0"
	}

	// buildAndStart builds a fresh controller-manager (cache + multicluster
	// client + controllers) and runs it to completion. It is invoked once by the
	// coupled path and repeatedly by the supervisor in self-heal mode. Transient
	// errors (e.g. a lost apiserver connection) are returned rather than exiting
	// the process, so the supervisor can back off and retry. In coupled mode it
	// also binds the API server and health checks to the manager so a manager
	// failure exits the process, as before.
	buildAndStart := func(ctx context.Context) error {
		mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
			Scheme:                 scheme,
			Metrics:                metricsServerOptions,
			WebhookServer:          webhookServer,
			HealthProbeBindAddress: managerProbeAddr,
			// Kept for consistency with kubebuilder scaffold, but the shim should
			// always run with leader election disabled.
			LeaderElection: enableLeaderElection,
		})
		if err != nil {
			return fmt.Errorf("unable to create manager: %w", err)
		}

		multiclusterClient, err := setupMulticlusterClient(ctx, mgr, restConfig, multiclusterMonitor)
		if err != nil {
			return fmt.Errorf("unable to set up multicluster client: %w", err)
		}

		if placementShim != nil {
			if err := placementShim.SetupControllerWithManager(ctx, mgr, multiclusterClient); err != nil {
				return fmt.Errorf("unable to set up placement shim controller: %w", err)
			}
			// Drive the shim's cache-readiness flag: mark ready once this
			// manager's cache has synced, and clear it when the cycle returns so
			// cache-backed handlers degrade to 503 while the manager is down or
			// restarting. Passthrough handlers ignore the flag.
			//
			// A per-cycle context (cancelled on return) stops WaitForCacheSync from
			// blocking across restarts. The mutex + cacheCtx.Err() re-check under
			// the lock closes the ordering race where the sync goroutine has just
			// observed a synced cache but the cycle is returning: cancelCacheSync
			// runs before the defer takes the lock, so whichever critical section
			// runs first, the goroutine only sets ready=true while the context is
			// still live, and the deferred ready=false always wins the final state.
			var readyMu sync.Mutex
			cacheCtx, cancelCacheSync := context.WithCancel(ctx)
			defer func() {
				cancelCacheSync()
				readyMu.Lock()
				placementShim.SetManagerReady(false)
				readyMu.Unlock()
			}()
			go func() {
				if mgr.GetCache().WaitForCacheSync(cacheCtx) {
					readyMu.Lock()
					if cacheCtx.Err() == nil {
						placementShim.SetManagerReady(true)
					}
					readyMu.Unlock()
				}
			}()
		}

		if metricsCertWatcher != nil {
			if err := mgr.Add(metricsCertWatcher); err != nil {
				return fmt.Errorf("unable to add metrics certificate watcher: %w", err)
			}
		}
		if webhookCertWatcher != nil {
			if err := mgr.Add(webhookCertWatcher); err != nil {
				return fmt.Errorf("unable to add webhook certificate watcher: %w", err)
			}
		}

		if !selfHeal {
			// Coupled mode: the manager owns the health checks and the API server,
			// so the process shares the manager's lifecycle.
			if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
				return fmt.Errorf("unable to set up health check: %w", err)
			}
			if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
				return fmt.Errorf("unable to set up ready check: %w", err)
			}
			if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
				setupLog.Info("starting api server", "address", apiBindAddr)
				return httpext.ListenAndServeContext(ctx, apiBindAddr, mux)
			})); err != nil {
				return fmt.Errorf("unable to add api server to manager: %w", err)
			}
		}

		setupLog.Info("starting manager")
		return mgr.Start(ctx)
	}

	// +kubebuilder:scaffold:builder

	if selfHeal {
		// Self-healing mode: the REST API, liveness probe, and metrics endpoint
		// are bound once in the outer process and survive manager restarts, while
		// the supervisor rebuilds the manager (and its cache) with backoff on
		// failure. The pod never crashes on apiserver/cache connectivity issues.
		if err := supervisor.Run(ctx, supervisor.Options{
			ProbeAddr:       probeAddr,
			APIAddr:         apiBindAddr,
			MetricsAddr:     managerMetricsAddr,
			MetricsGatherer: metrics.Registry,
			Mux:             mux,
			ManagerUp:       managerUp,
			BuildAndStart:   buildAndStart,
		}); err != nil {
			setupLog.Error(err, "supervisor exited with error")
			os.Exit(1)
		}
		return
	}

	// Coupled mode (--self-heal=false): a manager failure exits the process.
	// This is the classic behavior kept for parity.
	managerUp.Set(1)
	if err := buildAndStart(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func setupMulticlusterClient(ctx context.Context, mgr manager.Manager, restConfig *rest.Config, monitor multicluster.Monitor) (*multicluster.Client, error) {
	homeCluster, err := cluster.New(restConfig, func(o *cluster.Options) { o.Scheme = scheme })
	if err != nil {
		return nil, fmt.Errorf("unable to create home cluster: %w", err)
	}
	if err := mgr.Add(homeCluster); err != nil {
		return nil, fmt.Errorf("unable to add home cluster: %w", err)
	}
	mcl := &multicluster.Client{
		HomeCluster:     homeCluster,
		HomeRestConfig:  restConfig,
		HomeScheme:      scheme,
		ResourceRouters: multicluster.DefaultResourceRouters,
		Monitor:         monitor,
	}
	mclConfig := conf.GetConfigOrDie[multicluster.ClientConfig]()
	if err := mcl.InitFromConf(ctx, mgr, mclConfig); err != nil {
		return nil, fmt.Errorf("unable to initialize multicluster client: %w", err)
	}
	return mcl, nil
}
