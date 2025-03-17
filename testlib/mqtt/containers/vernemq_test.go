// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package containers

import (
	"os"
	"testing"

	_ "github.com/lib/pq"
)

func TestVernemqContainer_Init(t *testing.T) {
	if os.Getenv("VERNEMQ_CONTAINER") != "1" {
		t.Skip("skipping test; set VERNEMQ_CONTAINER=1 to run")
	}

	container := VernemqContainer{}
	container.Init(t)

	// Should not panic.

	container.Close()
}
