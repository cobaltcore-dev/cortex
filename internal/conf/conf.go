// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"os"
	"sync"

	"github.com/cobaltcore-dev/cortex/internal/logging"
)

// Configuration values for the application.
type Config struct {
	OSAuthURL           string // URL to the OpenStack Keystone authentication endpoint.
	OSUsername          string
	OSPassword          string
	OSProjectName       string
	OSUserDomainName    string
	OSProjectDomainName string
	PrometheusURL       string
	DBHost              string
	DBPort              string
	DBUser              string
	DBPass              string
}

// Retrieve the value of the environment variable named by the key.
// If the variable is empty, it logs an error and exits the application.
func forceGetenv(key string) string {
	value := os.Getenv(key)
	if value == "" {
		logging.Log.Error("missing environment variable", "key", key)
		panic("missing environment variable")
	}
	return value
}

// Retrieve the value of the environment variable named by the key.
// If the variable is empty, it returns the provided default value.
func getenv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// The current configuration values for the application.
// Use Get() to retrieve the values.
var config *Config

var loadLock = &sync.Mutex{}

// Load the configuration values from environment variables.
func loadFromEnv() {
	if config != nil {
		return
	}
	config = &Config{
		OSAuthURL:           forceGetenv("OS_AUTH_URL"),
		OSUsername:          forceGetenv("OS_USERNAME"),
		OSPassword:          forceGetenv("OS_PASSWORD"),
		OSProjectName:       forceGetenv("OS_PROJECT_NAME"),
		OSUserDomainName:    forceGetenv("OS_USER_DOMAIN_NAME"),
		OSProjectDomainName: forceGetenv("OS_PROJECT_DOMAIN_NAME"),
		PrometheusURL:       forceGetenv("PROMETHEUS_URL"),
		DBHost:              getenv("POSTGRES_HOST", "localhost"),
		DBPort:              getenv("POSTGRES_PORT", "5432"),
		DBUser:              getenv("POSTGRES_USER", "postgres"),
		DBPass:              getenv("POSTGRES_PASSWORD", "secret"),
	}
}

// Return the configuration values. If the configuration is not already loaded,
// it loads the values from environment variables.
func Get() *Config {
	if config == nil {
		// Don't load from env twice.
		loadLock.Lock()
		defer loadLock.Unlock()
		loadFromEnv()
	}
	return config
}
