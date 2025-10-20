// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package datasources

import (
	"context"
	"sync"
	"testing"
	"time"
)

// MockDatasource implements the Datasource interface for testing purposes.
type MockDatasource struct {
	InitCalled bool
	SyncCalled bool
	mu         sync.Mutex
}

func (m *MockDatasource) Init(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.InitCalled = true
}

func (m *MockDatasource) Sync(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SyncCalled = true
}

func TestPipeline(t *testing.T) {
	// Create mock datasources
	ds1 := &MockDatasource{}
	ds2 := &MockDatasource{}

	// Create a pipeline with the mock datasources
	pipeline := &Pipeline{
		Syncers: []Datasource{ds1, ds2},
	}

	// Test Init method
	ctx := t.Context()
	pipeline.Init(ctx)

	if !ds1.InitCalled || !ds2.InitCalled {
		t.Errorf("Init was not called on all datasources")
	}

	// Test SyncPeriodic method
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Run SyncPeriodic in a separate goroutine
	go func() {
		pipeline.SyncPeriodic(ctx)
	}()

	// Allow some time for SyncPeriodic to run
	time.Sleep(100 * time.Millisecond)
	cancel() // Stop the SyncPeriodic loop

	// Check if Sync was called on all datasources
	if !ds1.SyncCalled || !ds2.SyncCalled {
		t.Errorf("Sync was not called on all datasources")
	}
}
