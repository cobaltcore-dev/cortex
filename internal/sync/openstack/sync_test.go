// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

type MockObjectsAPI[M OpenStackModel, L OpenStackList] struct {
	list []M
}

func (m *MockObjectsAPI[M, L]) List(auth KeystoneAuth) ([]M, error) {
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
	//lint:ignore U1000 tableName is used by go-pg.
	tableName struct{} `pg:"openstack_mock_table"`
	ID        string   `json:"id" pg:"id,notnull,pk"`
	Val       string   `json:"val" pg:"val"`
}

func (m MockTable) GetName() string    { return "mock_table" }
func (m MockTable) GetPKField() string { return "id" }

type MockList struct {
	URL    string
	Links  *[]PageLink
	Models any
}

func (m MockList) GetURL() string        { return m.URL }
func (m MockList) GetLinks() *[]PageLink { return m.Links }
func (m MockList) GetModels() any        { return m.Models }

func TestSyncer_Init(t *testing.T) {
	mockDB := testlibDB.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	syncer := newSyncerOfType[MockTable, MockList](&mockDB, conf.SyncOpenStackConfig{}, sync.Monitor{})
	syncer.Init()

	// Verify the table was created
	if _, err := mockDB.Get().Model((*MockTable)(nil)).Exists(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestSyncer_Sync(t *testing.T) {
	mockDB := testlibDB.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	syncer := &syncer[MockTable, MockList]{
		API: &MockObjectsAPI[MockTable, MockList]{list: []MockTable{
			{ID: "1", Val: "Test"}, {ID: "2", Val: "Test2"},
		}},
		DB: &mockDB,
	}
	syncer.Init()

	err := syncer.Sync(KeystoneAuth{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Check if the objects were inserted
	count, err := mockDB.Get().Model((*MockTable)(nil)).Count()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 objects, got %d", count)
	}
}
