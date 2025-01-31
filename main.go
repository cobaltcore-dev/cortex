// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features"
	"github.com/cobaltcore-dev/cortex/internal/logging"
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack"
	"github.com/cobaltcore-dev/cortex/internal/sync/prometheus"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Periodically fetch data from the datasources and insert it into the database.
func runSyncer(registry *monitoring.Registry, config conf.Config, db db.DB) {
	monitor := sync.NewSyncMonitor(registry)
	syncers := []sync.Datasource{
		prometheus.NewCombinedSyncer(config, db, monitor),
		openstack.NewSyncer(config, db, monitor),
	}
	for _, syncer := range syncers {
		syncer.Init()
	}
	for {
		for _, syncer := range syncers {
			syncer.Sync()
		}
		time.Sleep(time.Minute * 1)
	}
}

// Periodically extract features from the database.
func runExtractor(registry *monitoring.Registry, config conf.Config, db db.DB) {
	monitor := features.NewPipelineMonitor(registry)
	pipeline := features.NewPipeline(config, db, monitor)
	pipeline.Init()
	for {
		pipeline.Extract()
		time.Sleep(time.Minute * 1)
	}
}

// Run a webserver that listens for external scheduling requests.
func runScheduler(registry *monitoring.Registry, config conf.Config, db db.DB) {
	monitor := scheduler.NewSchedulerMonitor(registry)
	api := scheduler.NewExternalSchedulingAPI(config, db, monitor)
	mux := http.NewServeMux()
	mux.HandleFunc("/up", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc(
		api.GetNovaExternalSchedulerURL(),
		api.NovaExternalScheduler,
	)
	logging.Log.Info("api listening on :8080")
	server := &http.Server{
		Addr:         ":8080",
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  90 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		panic(err)
	}
}

// Run the prometheus metrics server for monitoring.
func runMonitoringServer(registry *monitoring.Registry) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	logging.Log.Info("metrics listening on :2112")
	server := &http.Server{
		Addr:         ":2112",
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  90 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		panic(err)
	}
}

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		// Called by the Dockerfile build to make sure
		// all binaries can be executed
		if args[0] == "--version" {
			fmt.Printf("%s version %s", "cortex", "0.0.1")
			os.Exit(0)
		}
	}

	config := conf.NewConfig()
	if err := config.Validate(); err != nil {
		logging.Log.Error("failed to validate config", "error", err)
		os.Exit(1)
	}
	logging.Log.Info("config validated")

	db := db.NewDB()
	db.Init()
	defer db.Close()

	registry := monitoring.NewRegistry(config)
	go runMonitoringServer(registry)
	go runSyncer(registry, config, db)
	go runExtractor(registry, config, db)
	go runScheduler(registry, config, db)
	select {}
}
