// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"log"
	"net/http"

	"github.com/cobaltcore-dev/cortex/internal/scheduler"
)

func main() {
	http.HandleFunc(
		scheduler.APINovaExternalSchedulerURL,
		scheduler.APINovaExternalSchedulerHandler,
	)
	log.Println("Starting server on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
