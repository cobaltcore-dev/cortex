// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package env

import (
	"log"
	"os"
)

func ForceGetenv(key string) string {
	value := os.Getenv(key)
	if value == "" {
		log.Fatalf("environment variable %s is not set", key)
	}
	return value
}
