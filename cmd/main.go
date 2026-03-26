// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	ironcorev1alpha1 "github.com/cobaltcore-dev/cortex/api/external/ironcore/v1alpha1"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/prometheus"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/cinder"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/external"

	schedulinglib "github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/machines"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/manila"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/pods"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/commitments"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/failover"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/monitoring"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	"github.com/cobaltcore-dev/cortex/pkg/task"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/sapcc/go-bits/httpext"
	"github.com/sapcc/go-bits/must"
	corev1 "k8s.io/api/core/v1"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(ironcorev1alpha1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(hv1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

type MainConfig struct {
	// ID used to identify leader election participants.
	LeaderElectionID string `json:"leaderElectionID,omitempty"`
	// List of enabled controllers.
	EnabledControllers []string `json:"enabledControllers"`
	// List of enabled tasks.
	EnabledTasks []string `json:"enabledTasks"`
}

//nolint:gocyclo
func main() {
	ctx := context.Background()
	mainConfig := conf.GetConfigOrDie[MainConfig]()
	restConfig := ctrl.GetConfigOrDie()

	// Custom entrypoint for scheduler e2e tests.
	if len(os.Args) == 2 {
		copts := client.Options{Scheme: scheme}
		client := must.Return(client.New(restConfig, copts))
		switch os.Args[1] {
		case "e2e-nova":
			novaChecksConfig := conf.GetConfigOrDie[nova.ChecksConfig]()
			nova.RunChecks(ctx, client, novaChecksConfig)
			return
		case "e2e-cinder":
			cinder.RunChecks(ctx, client)
			return
		case "e2e-manila":
			manilaChecksConfig := conf.GetConfigOrDie[manila.ChecksConfig]()
			manila.RunChecks(ctx, client, manilaChecksConfig)
			return
		case "e2e-commitments":
			commitmentsChecksConfig := conf.GetConfigOrDie[commitments.E2EChecksConfig]()
			commitments.RunCommitmentsE2EChecks(ctx, commitmentsChecksConfig)
			return
		}
	}

	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
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
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Log the main configuration
	setupLog.Info("loaded main configuration",
		"enabledControllers", mainConfig.EnabledControllers,
		"enabledTasks", mainConfig.EnabledTasks,
		"leaderElectionID", mainConfig.LeaderElectionID)

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
	webhookTLSOpts := tlsOpts

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
		TLSOpts:       tlsOpts,
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
			setupLog.Error(err, "to initialize metrics certificate watcher", "error", err)
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
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       mainConfig.LeaderElectionID,
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
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
	hvGVK := schema.GroupVersionKind{Group: "kvm.cloud.sap", Version: "v1", Kind: "Hypervisor"}
	reservationGVK := schema.GroupVersionKind{Group: "cortex.cloud", Version: "v1alpha1", Kind: "Reservation"}
	historyGVK := schema.GroupVersionKind{Group: "cortex.cloud", Version: "v1alpha1", Kind: "History"}
	multiclusterClient := &multicluster.Client{
		HomeCluster:    homeCluster,
		HomeRestConfig: restConfig,
		HomeScheme:     scheme,
		ResourceRouters: map[schema.GroupVersionKind]multicluster.ResourceRouter{
			hvGVK:          multicluster.HypervisorResourceRouter{},
			reservationGVK: multicluster.ReservationsResourceRouter{},
			historyGVK:     multicluster.HistoryResourceRouter{},
		},
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

	// TODO: Remove me after scheduling pipeline steps don't require DB connections anymore.
	metrics.Registry.MustRegister(&db.Monitor)

	// API endpoint.
	mux := http.NewServeMux()

	// The pipeline monitor is a bucket for all metrics produced during the
	// execution of individual steps (see step monitor below) and the overall
	// pipeline.
	// TODO: Only initialize me for scheduling domains that actually use pipelines.
	filterWeigherPipelineMonitor := schedulinglib.NewPipelineMonitor()
	metrics.Registry.MustRegister(&filterWeigherPipelineMonitor)
	detectorPipelineMonitor := schedulinglib.NewDetectorPipelineMonitor()
	metrics.Registry.MustRegister(&detectorPipelineMonitor)

	if slices.Contains(mainConfig.EnabledControllers, "nova-pipeline-controllers") {
		// Filter-weigher pipeline controller setup.
		filterWeigherController := &nova.FilterWeigherPipelineController{
			Monitor: filterWeigherPipelineMonitor,
		}
		// Inferred through the base controller.
		filterWeigherController.Client = multiclusterClient
		if err := filterWeigherController.SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "nova FilterWeigherPipelineController")
			os.Exit(1)
		}
		novaAPIConfig := conf.GetConfigOrDie[nova.HTTPAPIConfig]()
		nova.NewAPI(novaAPIConfig, filterWeigherController).Init(mux)

		// Detector pipeline controller setup.
		novaClient := nova.NewNovaClient()
		novaClientConfig := conf.GetConfigOrDie[nova.NovaClientConfig]()
		if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
			return novaClient.Init(ctx, multiclusterClient, novaClientConfig)
		})); err != nil {
			setupLog.Error(err, "unable to initialize nova client")
			os.Exit(1)
		}

		// Initialize commitments API for LIQUID interface (with Nova client for usage reporting)
		commitmentsConfig := conf.GetConfigOrDie[commitments.Config]()
		commitmentsAPI := commitments.NewAPIWithConfig(multiclusterClient, commitmentsConfig, novaClient)
		commitmentsAPI.Init(mux, metrics.Registry, ctrl.Log.WithName("commitments-api"))

		deschedulingsController := &nova.DetectorPipelineController{
			Monitor: detectorPipelineMonitor,
			Breaker: &nova.DetectorCycleBreaker{NovaClient: novaClient},
		}
		// Inferred through the base controller.
		deschedulingsController.Client = multiclusterClient
		if err := (deschedulingsController).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "nova DetectorPipelineController")
			os.Exit(1)
		}
		go deschedulingsController.CreateDeschedulingsPeriodically(ctx)
		// Deschedulings cleanup on startup
		if err := (&nova.DeschedulingsCleanup{
			Client: multiclusterClient,
			Scheme: mgr.GetScheme(),
		}).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "Cleanup")
			os.Exit(1)
		}

		// Webhook that validates all pipelines.
		novaPipelineWebhook := nova.NewPipelineWebhook()
		if err := novaPipelineWebhook.SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to setup nova pipeline webhook")
			os.Exit(1)
		}
	}
	if slices.Contains(mainConfig.EnabledControllers, "nova-deschedulings-executor") {
		setupLog.Info("enabling controller", "controller", "nova-deschedulings-executor")
		executorConfig := conf.GetConfigOrDie[nova.DeschedulingsExecutorConfig]()
		novaClient := nova.NewNovaClient()
		novaClientConfig := conf.GetConfigOrDie[nova.NovaClientConfig]()
		if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
			return novaClient.Init(ctx, multiclusterClient, novaClientConfig)
		})); err != nil {
			setupLog.Error(err, "unable to initialize nova client")
			os.Exit(1)
		}
		if err := (&nova.DeschedulingsExecutor{
			Client:     multiclusterClient,
			Scheme:     mgr.GetScheme(),
			Conf:       executorConfig,
			NovaClient: novaClient,
		}).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "DeschedulingsExecutor")
			os.Exit(1)
		}
	}
	if slices.Contains(mainConfig.EnabledControllers, "hypervisor-overcommit-controller") {
		hypervisorOvercommitController := &nova.HypervisorOvercommitController{}
		hypervisorOvercommitController.Client = multiclusterClient
		if err := hypervisorOvercommitController.SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller",
				"controller", "HypervisorOvercommitController")
			os.Exit(1)
		}
	}
	if slices.Contains(mainConfig.EnabledControllers, "manila-decisions-pipeline-controller") {
		setupLog.Info("enabling controller", "controller", "manila-decisions-pipeline-controller")
		controller := &manila.FilterWeigherPipelineController{
			Monitor: filterWeigherPipelineMonitor,
		}
		// Inferred through the base controller.
		controller.Client = multiclusterClient
		if err := (controller).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "DecisionReconciler")
			os.Exit(1)
		}
		manila.NewAPI(controller).Init(mux)

		// Webhook that validates all pipelines.
		manilaPipelineWebhook := manila.NewPipelineWebhook()
		if err := manilaPipelineWebhook.SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to setup manila pipeline webhook")
			os.Exit(1)
		}
	}
	if slices.Contains(mainConfig.EnabledControllers, "cinder-decisions-pipeline-controller") {
		setupLog.Info("enabling controller", "controller", "cinder-decisions-pipeline-controller")
		controller := &cinder.FilterWeigherPipelineController{
			Monitor: filterWeigherPipelineMonitor,
		}
		// Inferred through the base controller.
		controller.Client = multiclusterClient
		if err := (controller).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "DecisionReconciler")
			os.Exit(1)
		}
		cinder.NewAPI(controller).Init(mux)

		// Webhook that validates all pipelines.
		cinderPipelineWebhook := cinder.NewPipelineWebhook()
		if err := cinderPipelineWebhook.SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to setup cinder pipeline webhook")
			os.Exit(1)
		}
	}
	if slices.Contains(mainConfig.EnabledControllers, "ironcore-decisions-pipeline-controller") {
		setupLog.Info("enabling controller", "controller", "ironcore-decisions-pipeline-controller")
		controller := &machines.FilterWeigherPipelineController{
			Monitor: filterWeigherPipelineMonitor,
		}
		// Inferred through the base controller.
		controller.Client = multiclusterClient
		if err := (controller).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "DecisionReconciler")
			os.Exit(1)
		}

		// Webhook that validates all pipelines.
		ironcorePipelineWebhook := machines.NewPipelineWebhook()
		if err := ironcorePipelineWebhook.SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to setup ironcore pipeline webhook")
			os.Exit(1)
		}
	}
	if slices.Contains(mainConfig.EnabledControllers, "pods-decisions-pipeline-controller") {
		setupLog.Info("enabling controller", "controller", "pods-decisions-pipeline-controller")
		controller := &pods.FilterWeigherPipelineController{
			Monitor: filterWeigherPipelineMonitor,
		}
		// Inferred through the base controller.
		controller.Client = multiclusterClient
		if err := (controller).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "DecisionReconciler")
			os.Exit(1)
		}

		// Webhook that validates all pipelines.
		podsPipelineWebhook := pods.NewPipelineWebhook()
		if err := podsPipelineWebhook.SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to setup pods pipeline webhook")
			os.Exit(1)
		}
	}

	if slices.Contains(mainConfig.EnabledControllers, "committed-resource-reservations-controller") {
		setupLog.Info("enabling controller", "controller", "committed-resource-reservations-controller")
		monitor := reservations.NewMonitor(multiclusterClient)
		metrics.Registry.MustRegister(&monitor)
		commitmentsConfig := conf.GetConfigOrDie[commitments.Config]()
		commitmentsConfig.ApplyDefaults()

		if err := (&commitments.CommitmentReservationController{
			Client: multiclusterClient,
			Scheme: mgr.GetScheme(),
			Conf:   commitmentsConfig,
		}).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "CommitmentReservation")
			os.Exit(1)
		}
	}
	if slices.Contains(mainConfig.EnabledControllers, "datasource-controllers") {
		setupLog.Info("enabling controller", "controller", "datasource-controllers")
		monitor := datasources.NewMonitor()
		metrics.Registry.MustRegister(&monitor)
		if err := (&openstack.OpenStackDatasourceReconciler{
			Client:  multiclusterClient,
			Scheme:  mgr.GetScheme(),
			Monitor: monitor,
			Conf:    conf.GetConfigOrDie[openstack.OpenStackDatasourceReconcilerConfig](),
		}).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "OpenStackDatasourceReconciler")
			os.Exit(1)
		}
		if err := (&prometheus.PrometheusDatasourceReconciler{
			Client:  multiclusterClient,
			Scheme:  mgr.GetScheme(),
			Monitor: monitor,
			Conf:    conf.GetConfigOrDie[prometheus.PrometheusDatasourceReconcilerConfig](),
		}).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "PrometheusDatasourceReconciler")
			os.Exit(1)
		}
	}
	if slices.Contains(mainConfig.EnabledControllers, "knowledge-controllers") {
		setupLog.Info("enabling controller", "controller", "knowledge-controllers")
		monitor := extractor.NewMonitor()
		metrics.Registry.MustRegister(&monitor)
		if err := (&extractor.KnowledgeReconciler{
			Client:  multiclusterClient,
			Scheme:  mgr.GetScheme(),
			Monitor: monitor,
			Conf:    conf.GetConfigOrDie[extractor.KnowledgeReconcilerConfig](),
		}).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "KnowledgeReconciler")
			os.Exit(1)
		}
		if err := (&extractor.TriggerReconciler{
			Client: multiclusterClient,
			Scheme: mgr.GetScheme(),
			Conf:   conf.GetConfigOrDie[extractor.TriggerReconcilerConfig](),
		}).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "TriggerReconciler")
			os.Exit(1)
		}
	}
	if slices.Contains(mainConfig.EnabledControllers, "kpis-controller") {
		setupLog.Info("enabling controller", "controller", "kpis-controller")
		kpisControllerConfig := conf.GetConfigOrDie[kpis.ControllerConfig]()
		if err := (&kpis.Controller{
			Client: multiclusterClient,
			Config: kpisControllerConfig,
		}).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "KPIController")
			os.Exit(1)
		}
	}
	if slices.Contains(mainConfig.EnabledControllers, "failover-reservations-controller") {
		setupLog.Info("enabling controller", "controller", "failover-reservations-controller")
		failoverConfig := conf.GetConfigOrDie[failover.FailoverConfig]()
		failoverConfig.ApplyDefaults()

		// DatasourceName is still required - check after applying defaults
		if failoverConfig.DatasourceName == "" {
			setupLog.Error(nil, "failover-reservations-controller requires datasourceName to be configured")
			os.Exit(1)
		}

		// The scheduler client calls the nova external scheduler API to get placement decisions
		schedulerClient := reservations.NewSchedulerClient(failoverConfig.SchedulerURL)

		// Defer the initialization of PostgresReader until the manager starts
		// because the cache is not ready during setup
		if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
			// Create PostgresReader from the configured Datasource CRD
			// This runs after the cache is started
			postgresReader, err := external.NewPostgresReader(ctx, multiclusterClient, failoverConfig.DatasourceName)
			if err != nil {
				setupLog.Error(err, "unable to create postgres reader for failover controller",
					"datasourceName", failoverConfig.DatasourceName)
				return err
			}

			// Create NovaReader and DBVMSource
			novaReader := external.NewNovaReader(postgresReader)
			vmSource := failover.NewDBVMSource(novaReader)

			// Create the unified failover controller
			// It handles both:
			// 1. Watch-based per-reservation reconciliation (acknowledgment, validation)
			// 2. Periodic bulk VM processing (creating/assigning reservations)
			failoverController := failover.NewFailoverReservationController(
				multiclusterClient,
				vmSource,
				failoverConfig,
				schedulerClient,
			)

			// Set up the watch-based reconciler for per-reservation reconciliation
			if err := failoverController.SetupWithManager(mgr, multiclusterClient); err != nil {
				setupLog.Error(err, "unable to set up failover reservation controller")
				return err
			}

			setupLog.Info("failover-reservations-controller starting",
				"datasourceName", failoverConfig.DatasourceName,
				"schedulerURL", failoverConfig.SchedulerURL,
				"reconcileInterval", failoverConfig.ReconcileInterval,
				"revalidationInterval", failoverConfig.RevalidationInterval)

			// Start the controller's periodic reconciliation loop
			return failoverController.Start(ctx)
		})); err != nil {
			setupLog.Error(err, "unable to add failover controller to manager")
			os.Exit(1)
		}
		setupLog.Info("failover-reservations-controller registered",
			"datasourceName", failoverConfig.DatasourceName,
			"schedulerURL", failoverConfig.SchedulerURL,
			"reconcileInterval", failoverConfig.ReconcileInterval,
			"revalidationInterval", failoverConfig.RevalidationInterval,
			"trustHypervisorLocation", failoverConfig.TrustHypervisorLocation,
			"maxVMsToProcess", failoverConfig.MaxVMsToProcess)
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

	if slices.Contains(mainConfig.EnabledTasks, "commitments-sync-task") {
		setupLog.Info("starting commitments syncer")
		syncerMonitor := commitments.NewSyncerMonitor()
		must.Succeed(metrics.Registry.Register(syncerMonitor))
		syncer := commitments.NewSyncer(multiclusterClient, syncerMonitor)
		syncerConfig := conf.GetConfigOrDie[commitments.SyncerConfig]()
		syncerConfig.ApplyDefaults()
		if err := (&task.Runner{
			Client:   multiclusterClient,
			Interval: syncerConfig.SyncInterval,
			Name:     "commitments-sync-task",
			Run:      func(ctx context.Context) error { return syncer.SyncReservations(ctx) },
			Init:     func(ctx context.Context) error { return syncer.Init(ctx, syncerConfig) },
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to add commitments sync task to manager")
			os.Exit(1)
		}
	}
	if slices.Contains(mainConfig.EnabledTasks, "nova-history-cleanup-task") {
		setupLog.Info("starting nova history cleanup task")
		historyCleanupConfig := conf.GetConfigOrDie[nova.HistoryCleanupConfig]()
		if err := (&task.Runner{
			Client:   multiclusterClient,
			Interval: time.Hour,
			Name:     "nova-history-cleanup-task",
			Run: func(ctx context.Context) error {
				return nova.HistoryCleanup(ctx, multiclusterClient, historyCleanupConfig)
			},
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to add nova history cleanup task to manager")
			os.Exit(1)
		}
	}
	if slices.Contains(mainConfig.EnabledTasks, "manila-history-cleanup-task") {
		setupLog.Info("starting manila history cleanup task")
		historyCleanupConfig := conf.GetConfigOrDie[manila.HistoryCleanupConfig]()
		if err := (&task.Runner{
			Client:   multiclusterClient,
			Interval: time.Hour,
			Name:     "manila-history-cleanup-task",
			Run: func(ctx context.Context) error {
				return manila.HistoryCleanup(ctx, multiclusterClient, historyCleanupConfig)
			},
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to add manila history cleanup task to manager")
			os.Exit(1)
		}
	}
	if slices.Contains(mainConfig.EnabledTasks, "cinder-history-cleanup-task") {
		setupLog.Info("starting cinder history cleanup task")
		historyCleanupConfig := conf.GetConfigOrDie[cinder.HistoryCleanupConfig]()
		if err := (&task.Runner{
			Client:   multiclusterClient,
			Interval: time.Hour,
			Name:     "cinder-history-cleanup-task",
			Run: func(ctx context.Context) error {
				return cinder.HistoryCleanup(ctx, multiclusterClient, historyCleanupConfig)
			},
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to add cinder history cleanup task to manager")
			os.Exit(1)
		}
	}

	errchan := make(chan error)
	go func() {
		errchan <- func() error {
			setupLog.Info("starting api server", "address", ":8080")
			return httpext.ListenAndServeContext(ctx, ":8080", mux)
		}()
	}()
	go func() {
		if err := <-errchan; err != nil {
			setupLog.Error(err, "problem running api server")
			os.Exit(1)
		}
	}()

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
