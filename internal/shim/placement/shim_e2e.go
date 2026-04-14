// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"log"
	"time"
)

// e2eTest is a named end-to-end test registered by handler e2e files.
type e2eTest struct {
	name string
	run  func(ctx context.Context)
}

// e2eTests is populated by init() functions in the handle_*_e2e.go files.
var e2eTests []e2eTest

// RunE2E executes end-to-end tests for all placement shim handlers.
func RunE2E(ctx context.Context) {
	log.Printf("Running %d e2e test(s)", len(e2eTests))
	totalStart := time.Now()
	for i, test := range e2eTests {
		log.Printf("[%d/%d] Starting: %s", i+1, len(e2eTests), test.name)
		start := time.Now()
		test.run(ctx)
		log.Printf("[%d/%d] Done: %s (took: %d ms)", i+1, len(e2eTests), test.name, time.Since(start).Milliseconds())
	}
	log.Printf("All e2e tests passed (took: %d ms)", time.Since(totalStart).Milliseconds())
}
