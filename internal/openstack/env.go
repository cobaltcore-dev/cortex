package openstack

import (
	"log"
	"os"
)

func mustGetEnv(key string) string {
	value := os.Getenv(key)
	if value == "" {
		log.Fatalf("environment variable %s is not set", key)
	}
	return value
}
