// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import "context"

// Syncer manages the lifecycle of a ConfigMap-backed local store:
// creating the ConfigMap on startup, and running a periodic background
// sync from upstream placement.
type Syncer interface {
	// Init creates the ConfigMap if it does not exist. Called once during
	// Shim.Start before any requests are served.
	Init(ctx context.Context) error

	// Run starts the periodic background sync from upstream. Blocks until
	// ctx is cancelled. Called as a goroutine from Shim.Start.
	Run(ctx context.Context)
}
