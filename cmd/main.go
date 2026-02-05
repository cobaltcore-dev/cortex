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
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
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
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	ironcorev1alpha1 "github.com/cobaltcore-dev/cortex/api/delegation/ironcore/v1alpha1"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/prometheus"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/cinder"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/explanation"
	schedulinglib "github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/machines"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/manila"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/pods"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/commitments"
	reservationscontroller "github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/controller"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	"github.com/cobaltcore-dev/cortex/pkg/generated/clientset/versioned"
	"github.com/cobaltcore-dev/cortex/pkg/generated/informers/externalversions"
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

//nolint:gocyclo
func main() {
	ctx := context.Background()
	config := conf.GetConfigOrDie[conf.Config]()
	restConfig := ctrl.GetConfigOrDie()

	// Custom entrypoint for scheduler e2e tests.
	if len(os.Args) == 2 {
		copts := client.Options{Scheme: scheme}
		client := must.Return(client.New(restConfig, copts))
		switch os.Args[1] {
		case "e2e-nova":
			nova.RunChecks(ctx, client, config)
			return
		case "e2e-cinder":
			cinder.RunChecks(ctx, client, config)
			return
		case "e2e-manila":
			manila.RunChecks(ctx, client, config)
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
		LeaderElectionID:       config.LeaderElectionID,
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
	multiclusterClient := &multicluster.Client{
		HomeCluster:    homeCluster,
		HomeRestConfig: restConfig,
		HomeScheme:     scheme,
	}
	// Map the formatted gvk from the config to the actual gvk object so that we
	// can look up the right cluster for a given API server override.
	var gvksByConfStr = make(map[string]schema.GroupVersionKind)
	for gvk := range scheme.AllKnownTypes() {
		// This produces something like: "cortex.cloud/v1alpha1/Decision" which can
		// be used to look up the right cluster for a given API server override.
		formatted := gvk.GroupVersion().String() + "/" + gvk.Kind
		gvksByConfStr[formatted] = gvk
	}
	for gvkStr := range gvksByConfStr {
		setupLog.Info("scheme gvk registered", "gvk", gvkStr)
	}
	for _, override := range config.APIServerOverrides {
		// Check if we have any registered gvk for this API server override.
		gvk, ok := gvksByConfStr[override.GVK]
		if !ok {
			setupLog.Error(nil, "no registered objects for apiserver override resource",
				"apiserver", override.Host, "gvk", override.GVK)
			os.Exit(1)
		}
		cluster, err := multiclusterClient.AddRemote(ctx, override.Host, override.CACert, gvk)
		if err != nil {
			setupLog.Error(err, "unable to create cluster for apiserver override", "apiserver", override.Host)
			os.Exit(1)
		}
		// Also tell the manager about this cluster so that controllers can use it.
		// This will execute the cluster.Start function when the manager starts.
		if err := mgr.Add(cluster); err != nil {
			setupLog.Error(err, "unable to add cluster for apiserver override", "apiserver", override.Host)
			os.Exit(1)
		}
	}

	// Our custom monitoring registry can add prometheus labels to all metrics.
	// This is useful to distinguish metrics from different deployments.
	metrics.Registry = monitoring.WrapRegistry(metrics.Registry, config.Monitoring)

	// TODO: Remove me after scheduling pipeline steps don't require DB connections anymore.
	metrics.Registry.MustRegister(&db.Monitor)

	// API endpoint.
	mux := http.NewServeMux()

	// The pipeline monitor is a bucket for all metrics produced during the
	// execution of individual steps (see step monitor below) and the overall
	// pipeline.
	pipelineMonitor := schedulinglib.NewPipelineMonitor()
	metrics.Registry.MustRegister(&pipelineMonitor)

	if slices.Contains(config.EnabledControllers, "nova-decisions-pipeline-controller") {
		decisionController := &nova.FilterWeigherPipelineController{
			Monitor: pipelineMonitor,
			Conf:    config,
		}
		// Inferred through the base controller.
		decisionController.Client = multiclusterClient
		if err := (decisionController).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "DecisionReconciler")
			os.Exit(1)
		}
		httpAPIConf := conf.GetConfigOrDie[nova.HTTPAPIConfig]()
		nova.NewAPI(httpAPIConf, decisionController).Init(mux)
	}
	if slices.Contains(config.EnabledControllers, "nova-deschedulings-pipeline-controller") {
		// Deschedulings controller
		monitor := schedulinglib.NewDetectorPipelineMonitor()
		metrics.Registry.MustRegister(&monitor)
		deschedulingsController := &nova.DetectorPipelineController{
			Monitor:              monitor,
			Conf:                 config,
			DetectorCycleBreaker: nova.NewDetectorCycleBreaker(),
		}
		// Inferred through the base controller.
		deschedulingsController.Client = multiclusterClient
		if err := (deschedulingsController).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "DeschedulingsReconciler")
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
	}
	if slices.Contains(config.EnabledControllers, "manila-decisions-pipeline-controller") {
		controller := &manila.FilterWeigherPipelineController{
			Monitor: pipelineMonitor,
			Conf:    config,
		}
		// Inferred through the base controller.
		controller.Client = multiclusterClient
		if err := (controller).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "DecisionReconciler")
			os.Exit(1)
		}
		manila.NewAPI(config, controller).Init(mux)
	}
	if slices.Contains(config.EnabledControllers, "cinder-decisions-pipeline-controller") {
		controller := &cinder.FilterWeigherPipelineController{
			Monitor: pipelineMonitor,
			Conf:    config,
		}
		// Inferred through the base controller.
		controller.Client = multiclusterClient
		if err := (controller).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "DecisionReconciler")
			os.Exit(1)
		}
		cinder.NewAPI(config, controller).Init(mux)
	}
	if slices.Contains(config.EnabledControllers, "ironcore-decisions-pipeline-controller") {
		controller := &machines.FilterWeigherPipelineController{
			Monitor: pipelineMonitor,
			Conf:    config,
		}
		// Inferred through the base controller.
		controller.Client = multiclusterClient
		if err := (controller).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "DecisionReconciler")
			os.Exit(1)
		}
	}
	if slices.Contains(config.EnabledControllers, "pods-decisions-pipeline-controller") {
		clientset, err := kubernetes.NewForConfig(restConfig)
		if err != nil {
			setupLog.Error(err, "unable to create kubernetes clientset")
			os.Exit(1)
		}

		informerFactory := informers.NewSharedInformerFactory(clientset, 30*time.Second)

		customClientset, err := versioned.NewForConfig(restConfig)
		if err != nil {
			setupLog.Error(err, "unable to create custom clientset")
			os.Exit(1)
		}
		customInformerFactory := externalversions.NewSharedInformerFactory(customClientset, 30*time.Second)

		scheduler, err := pods.New(ctx, informerFactory, customInformerFactory)
		if err != nil {
			setupLog.Error(err, "unable to instantiate scheduler")
			os.Exit(1)
		}
		scheduler.Logger = ctrl.Log.WithName("pods-scheduler")
		scheduler.Client = multiclusterClient
		scheduler.Recorder = mgr.GetEventRecorder("pods-scheduler")

		controller := &pods.FilterWeigherPipelineController{
			Monitor: pipelineMonitor,
		}
		controller.Client = multiclusterClient

		scheduler.SetPipelineController(controller)

		if err := controller.SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "DecisionReconciler")
			os.Exit(1)
		}

		informerFactory.Start(ctx.Done())
		customInformerFactory.Start(ctx.Done())
		informerFactory.WaitForCacheSync(ctx.Done())
		customInformerFactory.WaitForCacheSync(ctx.Done())
		if err := scheduler.WaitForHandlersSync(ctx); err != nil {
			setupLog.Error(err, "could not sync all event handlers")
		}

		go func() {
			setupLog.Info("starting pods scheduler")
			scheduler.Run(ctx)
		}()
	}
	if slices.Contains(config.EnabledControllers, "explanation-controller") {
		// Setup a controller which will reconcile the history and explanation for
		// decision resources.
		explanationController := &explanation.Controller{
			Client: multiclusterClient,
			// The explanation controller is compatible with multiple scheduling
			// domains.
			SchedulingDomain: config.SchedulingDomain,
		}
		if err := explanationController.SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "ExplanationController")
			os.Exit(1)
		}
	}
	if slices.Contains(config.EnabledControllers, "reservations-controller") {
		monitor := reservationscontroller.NewControllerMonitor(multiclusterClient)
		metrics.Registry.MustRegister(&monitor)
		if err := (&reservationscontroller.ReservationReconciler{
			Client:           multiclusterClient,
			Scheme:           mgr.GetScheme(),
			Conf:             config,
			HypervisorClient: reservationscontroller.NewHypervisorClient(),
		}).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "Reservation")
			os.Exit(1)
		}
	}
	if slices.Contains(config.EnabledControllers, "datasource-controllers") {
		monitor := datasources.NewMonitor()
		metrics.Registry.MustRegister(&monitor)
		if err := (&openstack.OpenStackDatasourceReconciler{
			Client:  multiclusterClient,
			Scheme:  mgr.GetScheme(),
			Monitor: monitor,
			Conf:    config,
		}).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "OpenStackDatasourceReconciler")
			os.Exit(1)
		}
		if err := (&prometheus.PrometheusDatasourceReconciler{
			Client:  multiclusterClient,
			Scheme:  mgr.GetScheme(),
			Monitor: monitor,
			Conf:    config,
		}).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "PrometheusDatasourceReconciler")
			os.Exit(1)
		}
	}
	if slices.Contains(config.EnabledControllers, "knowledge-controllers") {
		monitor := extractor.NewMonitor()
		metrics.Registry.MustRegister(&monitor)
		if err := (&extractor.KnowledgeReconciler{
			Client:  multiclusterClient,
			Scheme:  mgr.GetScheme(),
			Monitor: monitor,
			Conf:    config,
		}).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "KnowledgeReconciler")
			os.Exit(1)
		}
		if err := (&extractor.TriggerReconciler{
			Client: multiclusterClient,
			Scheme: mgr.GetScheme(),
			Conf:   config,
		}).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "TriggerReconciler")
			os.Exit(1)
		}
	}
	if slices.Contains(config.EnabledControllers, "kpis-controller") {
		if err := (&kpis.Controller{
			Client: multiclusterClient,
			// The kpis controller is compatible with multiple scheduling
			// domains.
			SchedulingDomain: config.SchedulingDomain,
		}).SetupWithManager(mgr, multiclusterClient); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "KPIController")
			os.Exit(1)
		}
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

	if slices.Contains(config.EnabledTasks, "commitments-sync-task") {
		setupLog.Info("starting commitments syncer")
		syncer := commitments.NewSyncer(multiclusterClient)
		if err := (&task.Runner{
			Client:   multiclusterClient,
			Interval: time.Hour,
			Name:     "commitments-sync-task",
			Run:      func(ctx context.Context) error { return syncer.SyncReservations(ctx) },
			Init:     func(ctx context.Context) error { return syncer.Init(ctx, config) },
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to add commitments sync task to manager")
			os.Exit(1)
		}
	}
	if slices.Contains(config.EnabledTasks, "nova-decisions-cleanup-task") {
		setupLog.Info("starting nova decisions cleanup task")
		if err := (&task.Runner{
			Client:   multiclusterClient,
			Interval: time.Hour,
			Name:     "nova-decisions-cleanup-task",
			Run: func(ctx context.Context) error {
				return nova.DecisionsCleanup(ctx, multiclusterClient, config)
			},
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to add nova decisions cleanup task to manager")
			os.Exit(1)
		}
	}
	if slices.Contains(config.EnabledTasks, "manila-decisions-cleanup-task") {
		setupLog.Info("starting manila decisions cleanup task")
		if err := (&task.Runner{
			Client:   multiclusterClient,
			Interval: time.Hour,
			Name:     "manila-decisions-cleanup-task",
			Run: func(ctx context.Context) error {
				return manila.DecisionsCleanup(ctx, multiclusterClient, config)
			},
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to add manila decisions cleanup task to manager")
			os.Exit(1)
		}
	}
	if slices.Contains(config.EnabledTasks, "cinder-decisions-cleanup-task") {
		setupLog.Info("starting cinder decisions cleanup task")
		if err := (&task.Runner{
			Client:   multiclusterClient,
			Interval: time.Hour,
			Name:     "cinder-decisions-cleanup-task",
			Run: func(ctx context.Context) error {
				return cinder.DecisionsCleanup(ctx, multiclusterClient, config)
			},
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to add cinder decisions cleanup task to manager")
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
