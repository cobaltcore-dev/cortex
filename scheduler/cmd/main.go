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

	libconf "github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/lib/monitoring"
	"github.com/cobaltcore-dev/cortex/lib/mqtt"
	cinderAPIHTTP "github.com/cobaltcore-dev/cortex/scheduler/internal/cinder/api/http"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/conf"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/e2e/cinder"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/e2e/manila"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/e2e/nova"
	manilaAPIHTTP "github.com/cobaltcore-dev/cortex/scheduler/internal/manila/api/http"
	novaAPIHTTP "github.com/cobaltcore-dev/cortex/scheduler/internal/nova/api/http"
	"github.com/sapcc/go-api-declarations/bininfo"
	"github.com/sapcc/go-bits/httpext"
	"go.uber.org/automaxprocs/maxprocs"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Run the prometheus metrics server for monitoring.
func runMonitoringServer(ctx context.Context, registry *monitoring.Registry, config libconf.MonitoringConfig) {
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
  -e2e-nova   Run end-to-end tests for nova scheduling.
  -e2e-manila Run end-to-end tests for manila scheduling.
  -e2e-cinder Run end-to-end tests for cinder scheduling.

  modes:
  -scheduler-nova   Serve Nova scheduling requests with a http API.
  -scheduler-manila Serve Manila scheduling requests with a http API.
  -scheduler-cinder Serve Cinder scheduling requests with a http API.
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		panic("no arguments provided")
	}

	// If called with `--version`, report version and exit (the Dockerfile
	// uses this to check if the binary was built correctly)
	bininfo.HandleVersionArgument()

	config := libconf.GetConfigOrDie[*conf.Config]()
	config.LoggingConfig.SetDefaultLogger()

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
	registry := monitoring.NewRegistry(config.MonitoringConfig)

	dbMonitor := db.NewDBMonitor(registry)
	database := db.NewPostgresDB(ctx, config.DBConfig, registry, dbMonitor)
	defer database.Close()

	switch taskName {
	case "e2e-nova":
		nova.RunChecks(ctx, *config)
		return
	case "e2e-cinder":
		cinder.RunChecks(ctx, *config)
		return
	case "e2e-manila":
		manila.RunChecks(ctx, *config)
		return
	}

	go database.CheckLivenessPeriodically(ctx)
	go runMonitoringServer(ctx, registry, config.MonitoringConfig)

	// Run an api server that serves some basic endpoints and can be extended.
	mux := http.NewServeMux()
	mux.HandleFunc("/up", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mqttClient := mqtt.NewClient(mqtt.NewMQTTMonitor(registry))
	if err := mqttClient.Connect(); err != nil {
		panic("failed to connect to mqtt broker: " + err.Error())
	}

	var api interface{ Init(*http.ServeMux) }
	sc := config.SchedulerConfig
	switch taskName {
	case "scheduler-nova":
		api = novaAPIHTTP.NewAPI(sc, registry, database, mqttClient)
	case "scheduler-manila":
		api = manilaAPIHTTP.NewAPI(sc, registry, database, mqttClient)
	case "scheduler-cinder":
		api = cinderAPIHTTP.NewAPI(sc, registry, database, mqttClient)
	default:
		panic("unknown task")
	}
	api.Init(mux) // non-blocking

	// Run the api server after all other tasks have been started and
	// all http handlers have been registered to the mux.
	apiConf := config.APIConfig
	addr := fmt.Sprintf(":%d", apiConf.Port)
	if err := httpext.ListenAndServeContext(ctx, addr, mux); err != nil {
		panic(err)
	}
	slog.Info("api listening", "port", apiConf.Port)

	select {}
}
