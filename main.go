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

	openstackSyncer := openstack.NewSyncer()
	openstackSyncer.Init()
	prometheusSyncer := prometheus.NewSyncer()
	prometheusSyncer.Init()
	pipeline := features.NewFeatureExtractorPipeline()
	pipeline.Init()

	go func() {
		for {
			prometheusSyncer.Sync() // Catch up until now, may take a while.
			openstackSyncer.Sync()  // Get the current servers, hypervisors, etc.
			pipeline.Extract()      // Extract features from the data.
			time.Sleep(time.Minute * 1)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/up", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc(
		scheduler.APINovaExternalSchedulerURL,
		scheduler.NewExternalSchedulingAPI().Handler,
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
