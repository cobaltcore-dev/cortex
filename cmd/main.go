// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"

	"github.com/cobaltcore-dev/cortex/cmd/sim"
	"github.com/cobaltcore-dev/cortex/internal/logging"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		logging.Log.Error("usage: go run main.go [--noisy]")
		panic("invalid usage")
	}
	if args[0] == "--noisy" {
		sim.SimulateNoisyVMScheduling()
		os.Exit(0)
	}
}
