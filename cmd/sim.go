// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"

	"github.com/cobaltcore-dev/cortex/cmd/sim"
)

func main() {
	args := os.Args[1:]
	if args[0] == "--noisy" {
		sim.SimulateNoisyVMScheduling()
		os.Exit(0)
	}
	if args[0] == "--error" {
		sim.SimulateRequestError()
		os.Exit(0)
	}
	sim.SimulateVMScheduling()
	os.Exit(0)
}
