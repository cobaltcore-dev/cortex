// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/datasources/openstack"
	"github.com/cobaltcore-dev/cortex/internal/datasources/prometheus"
	"github.com/cobaltcore-dev/cortex/internal/features"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/go-pg/pg/v10"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		if args[0] == "--version" {
			fmt.Printf("%s version %s", "cortex", "0.0.1")
			os.Exit(0)
		}
	}

	c := conf.Get()
	db := pg.Connect(&pg.Options{
		Addr:     fmt.Sprintf("%s:%s", c.DBHost, c.DBPort),
		User:     c.DBUser,
		Password: c.DBPass,
		Database: "postgres",
	})
	defer db.Close()

	// Poll until the database is alive
	ctx := context.Background()
	for {
		if err := db.Ping(ctx); err == nil {
			break
		}
		time.Sleep(time.Second * 1)
	}

	openstack.Init(db)
	prometheus.Init(db)
	features.Init(db)

	go func() {
		for {
			prometheus.Sync(db)  // Catch up until now, may take a while.
			openstack.Sync(db)   // Get the current servers, hypervisors, etc.
			features.Extract(db) // Extract features from the data.
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
