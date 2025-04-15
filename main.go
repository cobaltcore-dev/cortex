// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	gosync "sync"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/commands/checks"
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features"
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	apihttp "github.com/cobaltcore-dev/cortex/internal/scheduler/api/http"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack"
	"github.com/cobaltcore-dev/cortex/internal/sync/prometheus"
	"github.com/sapcc/go-api-declarations/bininfo"
	"github.com/sapcc/go-bits/httpext"
	"github.com/sapcc/go-bits/jobloop"
	"go.uber.org/automaxprocs/maxprocs"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Periodically fetch data from the datasources and insert it into the database.
func runSyncer(ctx context.Context, registry *monitoring.Registry, config conf.SyncConfig, db db.DB) {
	monitor := sync.NewSyncMonitor(registry)
	syncers := []sync.Datasource{
		prometheus.NewCombinedSyncer(config.Prometheus, db, monitor),
		openstack.NewCombinedSyncer(ctx, config.OpenStack, monitor, db),
	}
	for _, syncer := range syncers {
		syncer.Init(ctx)
	}
	for {
		select {
		case <-ctx.Done():
			slog.Info("syncer shutting down")
			return
		default:
			var wg gosync.WaitGroup
			for _, syncer := range syncers {
				wg.Add(1)
				go func(syncer sync.Datasource) {
					defer wg.Done()
					syncer.Sync(ctx)
				}(syncer)
			}
			wg.Wait()
			time.Sleep(jobloop.DefaultJitter(time.Minute))
		}
	}
}

// Periodically extract features from the database.
func runExtractor(registry *monitoring.Registry, config conf.FeaturesConfig, db db.DB) {
	monitor := features.NewPipelineMonitor(registry)
	pipeline := features.NewPipeline(config, db, monitor)
	// Selects the extractors to run based on the config.
	pipeline.Init(features.SupportedExtractors)
	pipeline.ExtractOnTrigger()
}

// Run a webserver that listens for external scheduling requests.
func runScheduler(ctx context.Context, registry *monitoring.Registry, config conf.SchedulerConfig, db db.DB) {
	schedulerMonitor := scheduler.NewSchedulerMonitor(registry)
	schedulerPipeline := scheduler.NewPipeline(config, db, schedulerMonitor)
	apiMonitor := apihttp.NewSchedulerMonitor(registry)
	api := apihttp.NewAPI(config.API, schedulerPipeline, apiMonitor)
	api.Init(ctx)
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
		panic(fmt.Sprintf("usage: %s [checks | syncer | extractor | scheduler]", os.Args[0]))
	}

	dbInstance := db.NewPostgresDB(config.GetDBConfig())
	defer dbInstance.Close()

	migrater := db.NewMigrater(dbInstance)
	migrater.Migrate(true)

	// If we're running one-off tasks (commands), don't setup the monitoring server.
	//nolint:gocritic // We may add more tasks in the future.
	switch taskName {
	case "checks":
		checks.RunChecks(ctx, config)
		return
	}

	monitoringConfig := config.GetMonitoringConfig()
	registry := monitoring.NewRegistry(monitoringConfig)
	go runMonitoringServer(ctx, registry, monitoringConfig)

	switch taskName {
	case "syncer":
		go runSyncer(ctx, registry, config.GetSyncConfig(), dbInstance)
	case "extractor":
		go runExtractor(registry, config.GetFeaturesConfig(), dbInstance)
	case "scheduler":
		go runScheduler(ctx, registry, config.GetSchedulerConfig(), dbInstance)
	default:
		panic("unknown task")
	}
	select {}
}
