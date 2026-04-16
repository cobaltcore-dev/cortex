// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/shim/placement"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/monitoring"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/sapcc/go-bits/httpext"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
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

func main() {
	ctx := ctrl.SetupSignalHandler()

	restConfig := ctrl.GetConfigOrDie()

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

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Custom entrypoint for placement shim e2e tests.
	if runPlacementShimE2E {
		if err := placement.RunE2E(ctx); err != nil {
			setupLog.Error(err, "E2E tests failed")
			os.Exit(1)
		}
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

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		// Kept for consistency with kubebuilder scaffold, but the shim should
		// always run with leader election disabled.
		LeaderElection: enableLeaderElection,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	homeCluster, err := cluster.New(restConfig, func(o *cluster.Options) { o.Scheme = scheme })
	if err != nil {
		setupLog.Error(err, "unable to create home cluster")
		os.Exit(1)
	}
	if err := mgr.Add(homeCluster); err != nil {
		setupLog.Error(err, "unable to add home cluster")
		os.Exit(1)
	}
	multiclusterClient := &multicluster.Client{
		HomeCluster:     homeCluster,
		HomeRestConfig:  restConfig,
		HomeScheme:      scheme,
		ResourceRouters: multicluster.DefaultResourceRouters,
	}
	multiclusterClientConfig := conf.GetConfigOrDie[multicluster.ClientConfig]()
	if err := multiclusterClient.InitFromConf(ctx, mgr, multiclusterClientConfig); err != nil {
		setupLog.Error(err, "unable to initialize multicluster client")
		os.Exit(1)
	}

	// Our custom monitoring registry can add prometheus labels to all metrics.
	// This is useful to distinguish metrics from different deployments.
	metricsConfig := conf.GetConfigOrDie[monitoring.Config]()
	metrics.Registry = monitoring.WrapRegistry(metrics.Registry, metricsConfig)

	// API endpoint.
	mux := http.NewServeMux()
	var placementShim *placement.Shim
	if enablePlacementShim {
		placementShim = &placement.Shim{Client: multiclusterClient}
		setupLog.Info("Adding placement shim to manager")
		if err := placementShim.SetupWithManager(ctx, mgr); err != nil {
			setupLog.Error(err, "unable to set up placement shim")
			os.Exit(1)
		}
		metrics.Registry.MustRegister(placementShim)
		placementShim.RegisterRoutes(mux)
	}

	// +kubebuilder:scaffold:builder

	if metricsCertWatcher != nil {
		setupLog.Info("Adding metrics certificate watcher to manager")
		if err := mgr.Add(metricsCertWatcher); err != nil {
			setupLog.Error(err, "unable to add metrics certificate watcher to manager")
			os.Exit(1)
		}
	}

	if webhookCertWatcher != nil {
		setupLog.Info("Adding webhook certificate watcher to manager")
		if err := mgr.Add(webhookCertWatcher); err != nil {
			setupLog.Error(err, "unable to add webhook certificate watcher to manager")
			os.Exit(1)
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	// Couple the API server to the manager lifecycle so the informer cache is
	// available as soon as the mux starts, and graceful shutdown is handled by
	// the manager context cancellation.
	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		setupLog.Info("starting api server", "address", ":8080")
		return httpext.ListenAndServeContext(ctx, ":8080", mux)
	})); err != nil {
		setupLog.Error(err, "unable to add api server to manager")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
