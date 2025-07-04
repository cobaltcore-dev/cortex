// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/keystone"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/cobaltcore-dev/cortex/testlib/mqtt"
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
	combinedSyncer.Init(t.Context())

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
	combinedSyncer.Sync(t.Context())

	if !mockSyncer1.syncCalled {
		t.Fatal("expected mockSyncer1.Sync to be called")
	}
	if !mockSyncer2.syncCalled {
		t.Fatal("expected mockSyncer2.Sync to be called")
	}
}

func TestNewCombinedSyncer(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	monitor := sync.Monitor{}
	mqttClient := &mqtt.MockClient{}     // Mock or initialize as needed
	config := conf.SyncOpenStackConfig{} // Populate with test configuration
	keystoneAPI := keystone.NewKeystoneAPI(conf.KeystoneConfig{})

	combinedSyncer := NewCombinedSyncer(t.Context(), keystoneAPI, config, monitor, testDB, mqttClient)

	if combinedSyncer == nil {
		t.Fatal("expected NewCombinedSyncer to return a non-nil CombinedSyncer")
	}

	// Additional assertions can be added here to verify the state of the returned CombinedSyncer
}
