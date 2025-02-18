// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

type mockSyncer struct {
	initCalled bool
	syncCalled bool
}

func (m *mockSyncer) Init(ctx context.Context) {
	m.initCalled = true
}

func (m *mockSyncer) Sync(ctx context.Context) error {
	m.syncCalled = true
	return nil
}

func TestNewCombinedSyncer(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	config := conf.SyncOpenStackConfig{}
	monitor := sync.Monitor{}

	syncer := NewCombinedSyncer(context.Background(), config, monitor, testDB)
	if syncer == nil {
		t.Fatal("expected non-nil syncer")
	}
}

func TestCombinedSyncer_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	monitor := sync.Monitor{}

	mockSyncer1 := &mockSyncer{}
	mockSyncer2 := &mockSyncer{}
	syncers := []Syncer{mockSyncer1, mockSyncer2}

	combinedSyncer := CombinedSyncer{monitor: monitor, syncers: syncers}
	combinedSyncer.Init(context.Background())

	if !mockSyncer1.initCalled {
		t.Fatal("expected mockSyncer1.Init to be called")
	}
	if !mockSyncer2.initCalled {
		t.Fatal("expected mockSyncer2.Init to be called")
	}
}

func TestCombinedSyncer_Sync(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	monitor := sync.Monitor{}

	mockSyncer1 := &mockSyncer{}
	mockSyncer2 := &mockSyncer{}
	syncers := []Syncer{mockSyncer1, mockSyncer2}

	combinedSyncer := CombinedSyncer{monitor: monitor, syncers: syncers}
	combinedSyncer.Sync(context.Background())

	if !mockSyncer1.syncCalled {
		t.Fatal("expected mockSyncer1.Sync to be called")
	}
	if !mockSyncer2.syncCalled {
		t.Fatal("expected mockSyncer2.Sync to be called")
	}
}
