// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"log"
	"os"
)

type Config struct {
	OSAuthUrl           string
	OSUsername          string
	OSPassword          string
	OSProjectName       string
	OSUserDomainName    string
	OSProjectDomainName string
	PrometheusUrl       string
	DBHost              string
	DBPort              string
	DBUser              string
	DBPass              string
}

func forceGetenv(key string) string {
	value := os.Getenv(key)
	if value == "" {
		log.Fatalf("environment variable %s is not set", key)
	}
	return value
}

var config Config
var loaded bool

func load() Config {
	return Config{
		OSAuthUrl:           forceGetenv("OS_AUTH_URL"),
		OSUsername:          forceGetenv("OS_USERNAME"),
		OSPassword:          forceGetenv("OS_PASSWORD"),
		OSProjectName:       forceGetenv("OS_PROJECT_NAME"),
		OSUserDomainName:    forceGetenv("OS_USER_DOMAIN_NAME"),
		OSProjectDomainName: forceGetenv("OS_PROJECT_DOMAIN_NAME"),
		PrometheusUrl:       forceGetenv("PROMETHEUS_URL"),
		DBHost:              forceGetenv("POSTGRES_HOST"),
		DBPort:              forceGetenv("POSTGRES_PORT"),
		DBUser:              forceGetenv("POSTGRES_USER"),
		DBPass:              forceGetenv("POSTGRES_PASSWORD"),
	}
}

func Get() Config {
	if !loaded {
		config = load()
		loaded = true
	}
	return config
}
