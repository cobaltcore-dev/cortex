// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/cobaltcore-dev/cortex/internal/datasources/openstack"
	"github.com/cobaltcore-dev/cortex/internal/datasources/prometheus"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"

	_ "github.com/lib/pq"
)

// Environment variables
var (
	osAuthUrl           string
	osUsername          string
	osPassword          string
	osProjectName       string
	osUserDomainName    string
	osProjectDomainName string
	prometheusUrl       string
	dbHost              string
	dbPort              string
	dbUser              string
	dbPass              string
)

func loadEnv() {
	osAuthUrl = os.Getenv("OS_AUTH_URL")
	osUsername = os.Getenv("OS_USERNAME")
	osPassword = os.Getenv("OS_PASSWORD")
	osProjectName = os.Getenv("OS_PROJECT_NAME")
	osUserDomainName = os.Getenv("OS_USER_DOMAIN_NAME")
	osProjectDomainName = os.Getenv("OS_PROJECT_DOMAIN_NAME")
	prometheusUrl = os.Getenv("PROMETHEUS_URL")
	dbHost = os.Getenv("POSTGRES_HOST")
	dbPort = os.Getenv("POSTGRES_PORT")
	dbUser = os.Getenv("POSTGRES_USER")
	dbPass = os.Getenv("POSTGRES_PASSWORD")
}

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		if args[0] == "--version" {
			fmt.Printf("%s version %s", "cortex", "0.0.1")
			os.Exit(0)
		}
	}

	loadEnv()

	openStackConf := openstack.OpenStackSyncConfig{
		OSAuthUrl:           osAuthUrl,
		OSUsername:          osUsername,
		OSPassword:          osPassword,
		OSProjectName:       osProjectName,
		OSUserDomainName:    osUserDomainName,
		OSProjectDomainName: osProjectDomainName,
		DbHost:              dbHost,
		DbPort:              dbPort,
		DbUser:              dbUser,
		DbPass:              dbPass,
	}
	go openstack.SyncPeriodic(openStackConf)

	conf := prometheus.PrometheusSyncConfig{
		PrometheusUrl: prometheusUrl,
		DbHost:        dbHost,
		DbPort:        dbPort,
		DbUser:        dbUser,
		DbPass:        dbPass,
	}
	go prometheus.SyncPeriodic(conf)

	http.HandleFunc(
		scheduler.APINovaExternalSchedulerURL,
		scheduler.APINovaExternalSchedulerHandler,
	)
	log.Println("Starting server on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
