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
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack"
	"github.com/cobaltcore-dev/cortex/internal/sync/prometheus"
)

// Periodically fetch data from the datasources and insert it into the database.
func runSyncer(config conf.Config, db db.DB) {
	datasources := prometheus.NewSyncers(config, db)
	datasources = append(datasources, openstack.NewSyncer(config, db))
	for _, ds := range datasources {
		ds.Init()
	}
	for {
		for _, ds := range datasources {
			ds.Sync()
		}
		time.Sleep(time.Minute * 1)
	}
}

// Periodically extract features from the database.
func runExtractor(config conf.Config, db db.DB) {
	pipeline := features.NewPipeline(config, db)
	pipeline.Init()
	for {
		pipeline.Extract()
		time.Sleep(time.Minute * 1)
	}
}

// Run a webserver that listens for external scheduling requests.
func runScheduler(config conf.Config, db db.DB) {
	api := scheduler.NewExternalSchedulingAPI(config, db)
	mux := http.NewServeMux()
	mux.HandleFunc("/up", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc(
		api.GetNovaExternalSchedulerURL(),
		api.NovaExternalScheduler,
	)
	logging.Log.Info("Listening on :8080")
	server := &http.Server{
		Addr:         ":8080",
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  15 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		logging.Log.Error("failed to start server", "error", err)
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

	go runSyncer(config, db)
	go runExtractor(config, db)
	go runScheduler(config, db)
	select {}
}
