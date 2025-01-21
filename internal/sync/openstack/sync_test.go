// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"errors"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/testlib"
	"github.com/go-pg/pg/v10"
)

type mockServerAPI struct {
	servers []OpenStackServer
	err     error
}

func (m *mockServerAPI) Get(auth openStackKeystoneAuth, url *string) (*openStackServerList, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &openStackServerList{Servers: m.servers}, nil
}

type mockHypervisorAPI struct {
	hypervisors []OpenStackHypervisor
	err         error
}

func (m *mockHypervisorAPI) Get(auth openStackKeystoneAuth, url *string) (*openStackHypervisorList, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &openStackHypervisorList{Hypervisors: m.hypervisors}, nil
}

type mockKeyStoneAPI struct {
	auth openStackKeystoneAuth
	err  error
}

func (m *mockKeyStoneAPI) Authenticate() (*openStackKeystoneAuth, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &m.auth, nil
}

func TestSyncer_Init(t *testing.T) {
	mockDB := testlib.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	mockServerAPI := &mockServerAPI{
		servers: []OpenStackServer{
			{ID: "server1", Name: "test-server"},
		},
	}
	mockHypervisorAPI := &mockHypervisorAPI{
		hypervisors: []OpenStackHypervisor{
			{ID: 1, Hostname: "test-hypervisor"},
		},
	}
	mockKeyStoneAPI := &mockKeyStoneAPI{
		auth: openStackKeystoneAuth{},
	}
	serversEnabled := true
	hypervisorsEnabled := true
	syncer := &syncer{
		Config: conf.SyncOpenStackConfig{
			ServersEnabled:     &serversEnabled,
			HypervisorsEnabled: &hypervisorsEnabled,
		},
		ServerAPI:     mockServerAPI,
		HypervisorAPI: mockHypervisorAPI,
		KeystoneAPI:   mockKeyStoneAPI,
		DB:            &mockDB,
	}
	syncer.Init()

	// Verify the tables were created
	for _, model := range []any{(*OpenStackServer)(nil), (*OpenStackHypervisor)(nil)} {
		if _, err := mockDB.Get().Model(model).Exists(); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	}
}

func TestSyncer_Sync(t *testing.T) {
	mockDB := testlib.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	mockServerAPI := &mockServerAPI{
		servers: []OpenStackServer{
			{ID: "server1", Name: "test-server"},
		},
	}
	mockHypervisorAPI := &mockHypervisorAPI{
		hypervisors: []OpenStackHypervisor{
			{ID: 1, Hostname: "test-hypervisor"},
		},
	}
	mockKeyStoneAPI := &mockKeyStoneAPI{
		auth: openStackKeystoneAuth{},
	}
	serversEnabled := true
	hypervisorsEnabled := true
	syncer := &syncer{
		Config: conf.SyncOpenStackConfig{
			ServersEnabled:     &serversEnabled,
			HypervisorsEnabled: &hypervisorsEnabled,
		},
		ServerAPI:     mockServerAPI,
		HypervisorAPI: mockHypervisorAPI,
		KeystoneAPI:   mockKeyStoneAPI,
		DB:            &mockDB,
	}
	syncer.Init()
	syncer.Sync()

	// Verify the servers were inserted
	var servers []OpenStackServer
	if err := mockDB.Get().Model(&servers).Select(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(servers) != 1 {
		t.Errorf("expected 1 server, got %d", len(servers))
	}
	if servers[0].ID != "server1" {
		t.Errorf("expected server ID to be %s, got %s", "server1", servers[0].ID)
	}
	if servers[0].Name != "test-server" {
		t.Errorf("expected server name to be %s, got %s", "test-server", servers[0].Name)
	}

	// Verify the hypervisors were inserted
	var hypervisors []OpenStackHypervisor
	if err := mockDB.Get().Model(&hypervisors).Select(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(hypervisors) != 1 {
		t.Errorf("expected 1 hypervisor, got %d", len(hypervisors))
	}
	if hypervisors[0].ID != 1 {
		t.Errorf("expected hypervisor ID to be %d, got %d", 1, hypervisors[0].ID)
	}
	if hypervisors[0].Hostname != "test-hypervisor" {
		t.Errorf("expected hypervisor hostname to be %s, got %s", "test-hypervisor", hypervisors[0].Hostname)
	}
}

func TestSyncer_Sync_Failure(t *testing.T) {
	mockDB := testlib.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	// Mock the ServerAPI and HypervisorAPI to return errors
	mockServerAPI := &mockServerAPI{
		err: errors.New("failed to get servers"),
	}
	mockHypervisorAPI := &mockHypervisorAPI{
		err: errors.New("failed to get hypervisors"),
	}
	mockKeyStoneAPI := &mockKeyStoneAPI{
		auth: openStackKeystoneAuth{},
	}
	serversEnabled := true
	hypervisorsEnabled := true
	syncer := &syncer{
		Config: conf.SyncOpenStackConfig{
			ServersEnabled:     &serversEnabled,
			HypervisorsEnabled: &hypervisorsEnabled,
		},
		ServerAPI:     mockServerAPI,
		HypervisorAPI: mockHypervisorAPI,
		KeystoneAPI:   mockKeyStoneAPI,
		DB:            &mockDB,
	}
	syncer.Init()
	syncer.Sync()

	// Verify no servers were inserted
	var servers []OpenStackServer
	if err := mockDB.Get().Model(&servers).Select(); err != nil && errors.Is(err, pg.ErrNoRows) {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(servers) != 0 {
		t.Errorf("expected 0 servers, got %d", len(servers))
	}

	// Verify no hypervisors were inserted
	var hypervisors []OpenStackHypervisor
	if err := mockDB.Get().Model(&hypervisors).Select(); err != nil && errors.Is(err, pg.ErrNoRows) {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(hypervisors) != 0 {
		t.Errorf("expected 0 hypervisors, got %d", len(hypervisors))
	}
}
