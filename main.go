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

var log = logging.Default()

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		if args[0] == "--version" {
			fmt.Printf("%s version %s", "cortex", "0.0.1")
			os.Exit(0)
		}
	}

	db.Init()
	defer db.DB.Close()

	openstack.Init()
	prometheus.Init()
	features.Init()

	go func() {
		for {
			openstack.Sync()   // Get the current servers, hypervisors, etc.
			prometheus.Sync()  // Catch up until now, may take a while.
			features.Extract() // Extract features from the data.
			time.Sleep(time.Minute * 1)
		}
	}()

	http.HandleFunc(
		"/up",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	)
	http.HandleFunc(
		scheduler.APINovaExternalSchedulerURL,
		scheduler.APINovaExternalSchedulerHandler,
	)
	log.Info("Listening on :8080")
	http.ListenAndServe(":8080", nil)
}
