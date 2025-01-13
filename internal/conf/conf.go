// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"os"

	"github.com/cobaltcore-dev/cortex/internal/logging"
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
		logging.Log.Error("missing environment variable", "key", key)
		os.Exit(1)
	}
	return value
}

func getenv(key string, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
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
		DBHost:              getenv("POSTGRES_HOST", "postgres"),
		DBPort:              getenv("POSTGRES_PORT", "5432"),
		DBUser:              getenv("POSTGRES_USER", "postgres"),
		DBPass:              getenv("POSTGRES_PASSWORD", "secret"),
	}
}

func Get() Config {
	if !loaded {
		config = load()
		loaded = true
	}
	return config
}
