// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/datasources/openstack"
	"github.com/cobaltcore-dev/cortex/internal/datasources/prometheus"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features"
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

	db.Init()
	openstack.Init()
	prometheus.Init()
	features.Init()

	go func() {
		for {
			prometheus.Sync()  // Catch up until now, may take a while.
			openstack.Sync()   // Get the current servers, hypervisors, etc.
			features.Extract() // Extract features from the data.
			time.Sleep(time.Minute * 1)
		}
	}()

	http.HandleFunc(
		scheduler.APINovaExternalSchedulerURL,
		scheduler.APINovaExternalSchedulerHandler,
	)
	log.Println("Starting server on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
