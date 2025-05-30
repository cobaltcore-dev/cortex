// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sync

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/sapcc/go-bits/jobloop"
)

// Pipeline wrapper for all datasources.
type Pipeline struct {
	Syncers []Datasource
}

// Initialize all datasources.
func (p *Pipeline) Init(ctx context.Context) {
	for _, syncer := range p.Syncers {
		syncer.Init(ctx)
	}
}

// Sync all datasources in parallel.
func (p *Pipeline) SyncPeriodic(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			slog.Info("syncer shutting down")
			return
		default:
			var wg sync.WaitGroup
			for _, syncer := range p.Syncers {
				wg.Add(1)
				go func(syncer Datasource) {
					defer wg.Done()
					syncer.Sync(ctx)
				}(syncer)
			}
			wg.Wait()
			time.Sleep(jobloop.DefaultJitter(time.Minute))
		}
	}
}
