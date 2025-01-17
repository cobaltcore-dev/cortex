// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/datasources/openstack"
	"github.com/cobaltcore-dev/cortex/internal/datasources/prometheus"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features"
	"github.com/cobaltcore-dev/cortex/internal/logging"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		if args[0] == "--version" {
			fmt.Printf("%s version %s", "cortex", "0.0.1")
			os.Exit(0)
		}
	}

	db := db.NewDB()
	db.Init()
	defer db.Close()

	datasources := prometheus.NewSyncers(db)
	datasources = append(datasources, openstack.NewSyncer(db))
	for _, ds := range datasources {
		ds.Init()
	}

	pipeline := features.NewPipeline(db)
	pipeline.Init()

	go func() {
		for {
			for _, ds := range datasources {
				ds.Sync()
			}
			// Extract features from the data.
			pipeline.Extract()
			time.Sleep(time.Minute * 1)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/up", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc(
		scheduler.APINovaExternalSchedulerURL,
		scheduler.NewExternalSchedulingAPI(db).Handler,
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
