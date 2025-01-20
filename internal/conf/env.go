// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"os"

	"github.com/cobaltcore-dev/cortex/internal/logging"
)

type SecretDBConfig struct {
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
}

type SecretOpenStackConfig struct {
	OSAuthURL           string // URL to the OpenStack Keystone authentication endpoint.
	OSUsername          string
	OSPassword          string
	OSProjectName       string
	OSUserDomainName    string
	OSProjectDomainName string
}

type SecretPrometheusConfig struct {
	PrometheusURL string
}

type SecretConfig struct {
	SecretDBConfig
	SecretOpenStackConfig
	SecretPrometheusConfig
}

func NewSecretConfig() SecretConfig {
	return SecretConfig{
		SecretDBConfig: SecretDBConfig{
			DBHost:     Getenv("POSTGRES_HOST", "localhost"),
			DBPort:     Getenv("POSTGRES_PORT", "5432"),
			DBUser:     Getenv("POSTGRES_USER", "postgres"),
			DBPassword: Getenv("POSTGRES_PASSWORD", "secret"),
			DBName:     Getenv("POSTGRES_DB", "postgres"),
		},
		SecretOpenStackConfig: SecretOpenStackConfig{
			OSAuthURL:           ForceGetenv("OS_AUTH_URL"),
			OSUsername:          ForceGetenv("OS_USERNAME"),
			OSPassword:          ForceGetenv("OS_PASSWORD"),
			OSProjectName:       ForceGetenv("OS_PROJECT_NAME"),
			OSUserDomainName:    ForceGetenv("OS_USER_DOMAIN_NAME"),
			OSProjectDomainName: ForceGetenv("OS_PROJECT_DOMAIN_NAME"),
		},
		SecretPrometheusConfig: SecretPrometheusConfig{
			PrometheusURL: ForceGetenv("PROMETHEUS_URL"),
		},
	}
}

// Retrieve the value of the environment variable named by the key.
// If the variable is empty, it logs an error and exits the application.
func ForceGetenv(key string) string {
	value := os.Getenv(key)
	if value == "" {
		logging.Log.Error("missing environment variable", "key", key)
		panic("missing environment variable")
	}
	return value
}

// Retrieve the value of the environment variable named by the key.
// If the variable is empty, it returns the provided default value.
func Getenv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}
