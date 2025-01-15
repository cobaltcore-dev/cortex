// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package env

import (
	"os"

	"github.com/cobaltcore-dev/cortex/internal/logging"
)

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
