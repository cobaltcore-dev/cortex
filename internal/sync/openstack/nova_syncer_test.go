// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

type MockNovaAPI[M NovaModel, L NovaList] struct {
	list []M
}

func (m *MockNovaAPI[M, L]) List(auth KeystoneAuth) ([]M, error) {
	return m.list, nil
}

type MockKeystoneAPI struct{}

func (m *MockKeystoneAPI) Authenticate() (*KeystoneAuth, error) {
	return &KeystoneAuth{}, nil
}

type MockSyncer struct {
	initCalled bool
	syncCalled bool
}

func (m *MockSyncer) Init() {
	m.initCalled = true
}

func (m *MockSyncer) Sync(auth *KeystoneAuth) error {
	m.syncCalled = true
	return nil
}

type MockTable struct {
	ID  string `json:"id" db:"id,primarykey"`
	Val string `json:"val" db:"val"`
}

func (m MockTable) GetName() string   { return "mock_table" }
func (m MockTable) TableName() string { return "openstack_mock_table" }

type MockList struct {
	URL    string
	Links  *[]PageLink
	Models any
}

func (m MockList) GetURL() string        { return m.URL }
func (m MockList) GetLinks() *[]PageLink { return m.Links }
func (m MockList) GetModels() any        { return m.Models }

func TestSyncer_Init(t *testing.T) {
	testDB := testlibDB.NewSqliteTestDB(t)
	defer testDB.Close()

	syncer := newNovaSyncer[MockTable, MockList](*testDB.DB, conf.SyncOpenStackConfig{}, sync.Monitor{})
	syncer.Init()

	// Verify the table was created
	if !testDB.TableExists(MockTable{}) {
		t.Error("expected table to be created")
	}
}

func TestSyncer_Sync(t *testing.T) {
	testDB := testlibDB.NewSqliteTestDB(t)
	defer testDB.Close()

	syncer := &novaSyncer[MockTable, MockList]{
		API: &MockNovaAPI[MockTable, MockList]{list: []MockTable{
			{ID: "1", Val: "Test"}, {ID: "2", Val: "Test2"},
		}},
		DB: *testDB.DB,
	}
	syncer.Init()

	err := syncer.Sync(KeystoneAuth{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Check if the objects were inserted
	count, err := testDB.SelectInt("SELECT COUNT(*) FROM openstack_mock_table")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 objects, got %d", count)
	}
}
