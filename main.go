// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/cobaltcore-dev/cortex/commands/checks"
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	novaDescheduler "github.com/cobaltcore-dev/cortex/internal/descheduler/nova"
	"github.com/cobaltcore-dev/cortex/internal/extractor"
	"github.com/cobaltcore-dev/cortex/internal/keystone"
	"github.com/cobaltcore-dev/cortex/internal/kpis"
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/manila"
	manilaAPIHTTP "github.com/cobaltcore-dev/cortex/internal/scheduler/manila/api/http"
	novaScheduler "github.com/cobaltcore-dev/cortex/internal/scheduler/nova"
	novaApiHTTP "github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api/http"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack"
	"github.com/cobaltcore-dev/cortex/internal/sync/prometheus"
	"github.com/sapcc/go-api-declarations/bininfo"
	"github.com/sapcc/go-bits/httpext"
	"go.uber.org/automaxprocs/maxprocs"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Periodically fetch data from the datasources and insert it into the database.
func runSyncer(ctx context.Context, registry *monitoring.Registry, config conf.Config, db db.DB) {
	monitor := sync.NewSyncMonitor(registry)
	mqttClient := mqtt.NewClient(mqtt.NewMQTTMonitor(registry))
	if err := mqttClient.Connect(); err != nil {
		panic("failed to connect to mqtt broker: " + err.Error())
	}
	defer mqttClient.Disconnect()
	syncConfig := config.GetSyncConfig()
	keystoneAPI := keystone.NewKeystoneAPI(config.GetKeystoneConfig())
	syncers := []sync.Datasource{
		prometheus.NewCombinedSyncer(prometheus.SupportedSyncers, syncConfig.Prometheus, db, monitor, mqttClient),
		openstack.NewCombinedSyncer(ctx, keystoneAPI, syncConfig.OpenStack, monitor, db, mqttClient),
	}
	pipeline := sync.Pipeline{Syncers: syncers}
	pipeline.Init(ctx)
	go pipeline.SyncPeriodic(ctx) // blocking
}

// Periodically extract features from the database.
func runExtractor(registry *monitoring.Registry, config conf.ExtractorConfig, db db.DB) {
	monitor := extractor.NewPipelineMonitor(registry)

	mqttClient := mqtt.NewClient(mqtt.NewMQTTMonitor(registry))
	if err := mqttClient.Connect(); err != nil {
		panic("failed to connect to mqtt broker: " + err.Error())
	}
	defer mqttClient.Disconnect()

	pipeline := extractor.NewPipeline(config, db, monitor, mqttClient)
	// Selects the extractors to run based on the config.
	pipeline.Init(extractor.SupportedExtractors)
	go pipeline.ExtractOnTrigger() // blocking
}

// Run a webserver that listens for external Nova scheduling requests.
func runSchedulerNova(mux *http.ServeMux, registry *monitoring.Registry, config conf.SchedulerConfig, db db.DB) {
	monitor := scheduler.NewPipelineMonitor("nova", registry)
	mqttClient := mqtt.NewClient(mqtt.NewMQTTMonitor(registry))
	if err := mqttClient.Connect(); err != nil {
		panic("failed to connect to mqtt broker: " + err.Error())
	}
	defer mqttClient.Disconnect()
	schedulerPipeline := novaScheduler.NewPipeline(config, db, monitor, mqttClient)
	apiMonitor := scheduler.NewSchedulerMonitor(registry)
	api := novaApiHTTP.NewAPI(config.API, schedulerPipeline, apiMonitor)
	api.Init(mux) // non-blocking
}

// Run a webserver that listens for external scheduling requests.
func runSchedulerManila(mux *http.ServeMux, registry *monitoring.Registry, config conf.SchedulerConfig, db db.DB) {
	monitor := scheduler.NewPipelineMonitor("manila", registry)
	mqttClient := mqtt.NewClient(mqtt.NewMQTTMonitor(registry))
	if err := mqttClient.Connect(); err != nil {
		panic("failed to connect to mqtt broker: " + err.Error())
	}
	defer mqttClient.Disconnect()
	schedulerPipeline := manila.NewPipeline(config, db, monitor, mqttClient)
	apiMonitor := scheduler.NewSchedulerMonitor(registry)
	api := manilaAPIHTTP.NewAPI(config.API, schedulerPipeline, apiMonitor)
	api.Init(mux) // non-blocking
}

// Run a kpi service that periodically calculates kpis.
func runKPIService(registry *monitoring.Registry, config conf.KPIsConfig, db db.DB) {
	pipeline := kpis.NewPipeline(config)
	if err := pipeline.Init(kpis.SupportedKPIs, db, registry); err != nil {
		panic("failed to initialize kpi pipeline: " + err.Error())
	} // non-blocking
}

// Run a descheduler for Nova virtual machines.
func runDeschedulerNova(ctx context.Context, registry *monitoring.Registry, config conf.Config, db db.DB) {
	monitor := novaDescheduler.NewPipelineMonitor(registry)
	keystoneAPI := keystone.NewKeystoneAPI(config.GetKeystoneConfig())
	deschedulerConf := config.GetDeschedulerConfig()
	descheduler := novaDescheduler.NewDescheduler(deschedulerConf, monitor, keystoneAPI)
	descheduler.Init(novaDescheduler.SupportedSteps, ctx, db, deschedulerConf) // non-blocking
	go descheduler.DeschedulePeriodically(ctx)                                 // blocking
}

// Run the prometheus metrics server for monitoring.
func runMonitoringServer(ctx context.Context, registry *monitoring.Registry, config conf.MonitoringConfig) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	slog.Info("metrics listening", "port", config.Port)
	addr := fmt.Sprintf(":%d", config.Port)
	if err := httpext.ListenAndServeContext(ctx, addr, mux); err != nil {
		panic(err)
	}
}

// Message printed if cortex is started with unknown arguments.
const usage = `
  commands:
  -checks  Run end-to-end tests.
  -migrate Run database migrations.

  modes:
  -syncer    Sync data from external datasources into the database.
  -extractor Extract knowledge from the synced data and store it in the database.
  -scheduler-nova   Serve Nova scheduling requests with a http API.
  -scheduler-manila Serve Manila scheduling requests with a http API.
  -kpis      Expose KPIs extracted from the database.
  -descheduler-nova Run a Nova descheduler that periodically de-schedules VMs.
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		panic("no arguments provided")
	}

	// If called with `--version`, report version and exit (the Dockerfile
	// uses this to check if the binary was built correctly)
	bininfo.HandleVersionArgument()

	config := conf.NewConfig()
	// Set the configured logger.
	config.GetLoggingConfig().SetDefaultLogger()
	if err := config.Validate(); err != nil {
		slog.Error("failed to validate config", "error", err)
		panic(err)
	}
	slog.Info("config validated")

	// Set runtime concurrency to match CPU limit imposed by Kubernetes
	undoMaxprocs, err := maxprocs.Set(maxprocs.Logger(slog.Debug))
	if err != nil {
		panic(err)
	}
	defer undoMaxprocs()

	// Override User-Agent header for all requests made by this process
	// (logs will show e.g. "blueprint-api/d0c9faa" instead of "Go-http-client/2.0")
	wrap := httpext.WrapTransport(&http.DefaultTransport)
	wrap.SetOverrideUserAgent(bininfo.Component(), bininfo.VersionOr("rolling"))

	// This context will gracefully shutdown when the process receives the
	// standard shutdown signal SIGINT, with a 10-second delay to allow
	// Kubernetes to stop sending new requests well before the process starts
	// to shut down.
	ctx := httpext.ContextWithSIGINT(context.Background(), 10*time.Second)

	// Parse command line arguments.
	var taskName string
	if len(os.Args) == 2 {
		taskName = os.Args[1]
		bininfo.SetTaskName(taskName)
	} else {
		slog.Error("invalid arguments", "args", os.Args)
		panic(usage)
	}

	// Set up the monitoring registry and database connection.
	monitoringConfig := config.GetMonitoringConfig()
	registry := monitoring.NewRegistry(monitoringConfig)

	dbMonitor := db.NewDBMonitor(registry)
	database := db.NewPostgresDB(ctx, config.GetDBConfig(), registry, dbMonitor)
	defer database.Close()

	// Check if we want to perform one-time tasks like checks or migrations.
	switch taskName {
	case "checks":
		checks.RunChecks(ctx, config)
		return
	case "migrate":
		migrater := db.NewMigrater(database)
		migrater.Migrate(true)
		slog.Info("migrations executed")
		return
	}

	go database.CheckLivenessPeriodically(ctx)
	go runMonitoringServer(ctx, registry, monitoringConfig)

	// Run an api server that serves some basic endpoints and can be extended.
	mux := http.NewServeMux()
	mux.HandleFunc("/up", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	switch taskName {
	case "syncer":
		runSyncer(ctx, registry, config, database)
	case "extractor":
		runExtractor(registry, config.GetExtractorConfig(), database)
	case "scheduler-nova":
		runSchedulerNova(mux, registry, config.GetSchedulerConfig(), database)
	case "scheduler-manila":
		runSchedulerManila(mux, registry, config.GetSchedulerConfig(), database)
	case "kpis":
		runKPIService(registry, config.GetKPIsConfig(), database)
	case "descheduler-nova":
		runDeschedulerNova(ctx, registry, config, database)
	default:
		panic("unknown task")
	}

	// Run the api server after all other tasks have been started and
	// all http handlers have been registered to the mux.
	apiConf := config.GetAPIConfig()
	addr := fmt.Sprintf(":%d", apiConf.Port)
	if err := httpext.ListenAndServeContext(ctx, addr, mux); err != nil {
		panic(err)
	}
	slog.Info("api listening", "port", apiConf.Port)

	select {}
}
