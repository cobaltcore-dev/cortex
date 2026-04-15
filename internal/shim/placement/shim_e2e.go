// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"fmt"
	"time"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type e2eConfigRoot struct {
	E2E e2eConfig `yaml:"e2e"`
}

type e2eConfig struct {
	// SVCURL is the url placement shim service, e.g.
	// "http://cortex-placement-shim-service:8080"
	SVCURL string `json:"svcURL"`
}

// e2eTest is a named end-to-end test registered by handler e2e files.
type e2eTest struct {
	name string
	run  func(ctx context.Context) error
}

// e2eTests is populated by init() functions in the handle_*_e2e.go files.
var e2eTests []e2eTest

// RunE2E executes end-to-end tests for all placement shim handlers.
// It stops on the first failure and returns the error.
func RunE2E(ctx context.Context) error {
	log := logf.FromContext(ctx)
	log.Info("Running e2e test(s)", "count", len(e2eTests))
	totalStart := time.Now()
	for i, test := range e2eTests {
		log.Info("Starting e2e test",
			"index", i+1, "total", len(e2eTests), "name", test.name)
		start := time.Now()
		if err := test.run(ctx); err != nil {
			log.Error(err, "FAIL e2e test",
				"index", i+1, "total", len(e2eTests), "name", test.name,
				"took_ms", time.Since(start).Milliseconds())
			return fmt.Errorf("e2e test %q failed: %w", test.name, err)
		}
		log.Info("PASS e2e test",
			"index", i+1, "total", len(e2eTests), "name", test.name,
			"took_ms", time.Since(start).Milliseconds())
	}
	log.Info("All e2e tests passed",
		"took_ms", time.Since(totalStart).Milliseconds())
	return nil
}
