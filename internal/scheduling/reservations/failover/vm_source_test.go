// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"context"
	"errors"
	"strings"
	"testing"

	nova "github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDBVMSource_ListVMs(t *testing.T) {
	dbError := errors.New("connection refused: database unavailable")

	tests := []struct {
		name            string
		mock            *mockNovaReader
		wantErr         bool
		wantErrContains string
		wantWrappedErr  error
		wantVMCount     int
		wantFirstVMUUID string
	}{
		{
			name: "GetAllServers error",
			mock: &mockNovaReader{
				getAllServersFunc: func(ctx context.Context) ([]nova.Server, error) {
					return nil, dbError
				},
			},
			wantErr:         true,
			wantErrContains: "failed to get servers",
			wantWrappedErr:  dbError,
			wantVMCount:     0,
		},
		{
			name: "GetAllFlavors error",
			mock: &mockNovaReader{
				getAllServersFunc: func(ctx context.Context) ([]nova.Server, error) {
					return []nova.Server{{ID: "vm-1", FlavorName: "m1.large", OSEXTSRVATTRHost: "host1"}}, nil
				},
				getAllFlavorsFunc: func(ctx context.Context) ([]nova.Flavor, error) {
					return nil, dbError
				},
			},
			wantErr:         true,
			wantErrContains: "failed to get flavors",
			wantWrappedErr:  dbError,
			wantVMCount:     0,
		},
		{
			name: "success with multiple VMs",
			mock: &mockNovaReader{
				getAllServersFunc: func(ctx context.Context) ([]nova.Server, error) {
					return []nova.Server{
						{ID: "vm-1", FlavorName: "m1.large", OSEXTSRVATTRHost: "host1", TenantID: "project-1"},
						{ID: "vm-2", FlavorName: "m1.small", OSEXTSRVATTRHost: "host2", TenantID: "project-2"},
					}, nil
				},
				getAllFlavorsFunc: func(ctx context.Context) ([]nova.Flavor, error) {
					return []nova.Flavor{
						{Name: "m1.large", VCPUs: 4, RAM: 8192},
						{Name: "m1.small", VCPUs: 2, RAM: 4096},
					}, nil
				},
			},
			wantErr:         false,
			wantVMCount:     2,
			wantFirstVMUUID: "vm-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := NewDBVMSource(tt.mock)
			vms, err := source.ListVMs(context.Background())

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantErrContains != "" && !strings.Contains(err.Error(), tt.wantErrContains) {
					t.Errorf("expected error to contain %q, got: %v", tt.wantErrContains, err)
				}
				if tt.wantWrappedErr != nil && !errors.Is(err, tt.wantWrappedErr) {
					t.Errorf("expected error to wrap %v, got: %v", tt.wantWrappedErr, err)
				}
				if vms != nil {
					t.Errorf("expected nil VMs when error occurs, got %v", vms)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(vms) != tt.wantVMCount {
					t.Errorf("expected %d VMs, got %d", tt.wantVMCount, len(vms))
				}
				if tt.wantFirstVMUUID != "" && len(vms) > 0 && vms[0].UUID != tt.wantFirstVMUUID {
					t.Errorf("expected first VM UUID %q, got %q", tt.wantFirstVMUUID, vms[0].UUID)
				}
			}
		})
	}
}

func TestDBVMSource_GetVM(t *testing.T) {
	dbError := errors.New("connection refused: database unavailable")

	tests := []struct {
		name            string
		vmID            string
		mock            *mockNovaReader
		wantErr         bool
		wantErrContains string
		wantWrappedErr  error
		wantNilVM       bool
		wantVMUUID      string
		wantFlavorName  string
	}{
		{
			name: "GetServerByID error",
			vmID: "vm-1",
			mock: &mockNovaReader{
				getServerByIDFunc: func(ctx context.Context, serverID string) (*nova.Server, error) {
					return nil, dbError
				},
			},
			wantErr:         true,
			wantErrContains: "failed to get server",
			wantWrappedErr:  dbError,
			wantNilVM:       true,
		},
		{
			name: "GetFlavorByName error",
			vmID: "vm-1",
			mock: &mockNovaReader{
				getServerByIDFunc: func(ctx context.Context, serverID string) (*nova.Server, error) {
					return &nova.Server{ID: "vm-1", FlavorName: "m1.large", OSEXTSRVATTRHost: "host1"}, nil
				},
				getFlavorByNameFunc: func(ctx context.Context, flavorName string) (*nova.Flavor, error) {
					return nil, dbError
				},
			},
			wantErr:         true,
			wantErrContains: "failed to get flavor",
			wantWrappedErr:  dbError,
			wantNilVM:       true,
		},
		{
			name: "VM not found",
			vmID: "non-existent-vm",
			mock: &mockNovaReader{
				getServerByIDFunc: func(ctx context.Context, serverID string) (*nova.Server, error) {
					return nil, nil // Server not found
				},
			},
			wantErr:   false,
			wantNilVM: true,
		},
		{
			name: "success",
			vmID: "vm-1",
			mock: &mockNovaReader{
				getServerByIDFunc: func(ctx context.Context, serverID string) (*nova.Server, error) {
					return &nova.Server{
						ID:                    "vm-1",
						FlavorName:            "m1.large",
						OSEXTSRVATTRHost:      "host1",
						TenantID:              "project-1",
						OSEXTAvailabilityZone: "az-1",
					}, nil
				},
				getFlavorByNameFunc: func(ctx context.Context, flavorName string) (*nova.Flavor, error) {
					return &nova.Flavor{Name: "m1.large", VCPUs: 4, RAM: 8192}, nil
				},
			},
			wantErr:        false,
			wantNilVM:      false,
			wantVMUUID:     "vm-1",
			wantFlavorName: "m1.large",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := NewDBVMSource(tt.mock)
			vm, err := source.GetVM(context.Background(), tt.vmID)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantErrContains != "" && !strings.Contains(err.Error(), tt.wantErrContains) {
					t.Errorf("expected error to contain %q, got: %v", tt.wantErrContains, err)
				}
				if tt.wantWrappedErr != nil && !errors.Is(err, tt.wantWrappedErr) {
					t.Errorf("expected error to wrap %v, got: %v", tt.wantWrappedErr, err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			switch {
			case tt.wantNilVM:
				if vm != nil {
					t.Errorf("expected nil VM, got %v", vm)
				}
			case vm == nil:
				t.Fatal("expected VM, got nil")
			default:
				if tt.wantVMUUID != "" && vm.UUID != tt.wantVMUUID {
					t.Errorf("expected UUID %q, got %q", tt.wantVMUUID, vm.UUID)
				}
				if tt.wantFlavorName != "" && vm.FlavorName != tt.wantFlavorName {
					t.Errorf("expected FlavorName %q, got %q", tt.wantFlavorName, vm.FlavorName)
				}
			}
		})
	}
}

func TestDBVMSource_ListVMsOnHypervisors(t *testing.T) {
	dbError := errors.New("connection refused: database unavailable")

	tests := []struct {
		name           string
		mock           *mockNovaReader
		hypervisorList *hv1.HypervisorList
		wantErr        bool
		wantWrappedErr error
	}{
		{
			name: "propagates error from ListVMs",
			mock: &mockNovaReader{
				getAllServersFunc: func(ctx context.Context) ([]nova.Server, error) {
					return nil, dbError
				},
			},
			hypervisorList: &hv1.HypervisorList{
				Items: []hv1.Hypervisor{
					{ObjectMeta: metav1.ObjectMeta{Name: "host1"}},
				},
			},
			wantErr:        true,
			wantWrappedErr: dbError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := NewDBVMSource(tt.mock)
			vms, err := source.ListVMsOnHypervisors(context.Background(), tt.hypervisorList, false)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantWrappedErr != nil && !errors.Is(err, tt.wantWrappedErr) {
					t.Errorf("expected error to wrap %v, got: %v", tt.wantWrappedErr, err)
				}
				if vms != nil {
					t.Errorf("expected nil VMs when error occurs, got %v", vms)
				}
			}
		})
	}
}

func TestBuildVMsFromHypervisors(t *testing.T) {
	tests := []struct {
		name           string
		hypervisorList *hv1.HypervisorList
		postgresVMs    []VM
		wantCount      int
	}{
		{
			name: "empty postgres VMs",
			hypervisorList: &hv1.HypervisorList{
				Items: []hv1.Hypervisor{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "host1"},
						Status: hv1.HypervisorStatus{
							Instances: []hv1.Instance{
								{ID: "vm-1", Name: "vm-1", Active: true},
							},
						},
					},
				},
			},
			postgresVMs: []VM{},
			wantCount:   0,
		},
		{
			name:           "nil hypervisor items",
			hypervisorList: &hv1.HypervisorList{Items: nil},
			postgresVMs:    []VM{},
			wantCount:      0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("buildVMsFromHypervisors panicked: %v", r)
				}
			}()

			result := buildVMsFromHypervisors(tt.hypervisorList, tt.postgresVMs)

			if len(result) != tt.wantCount {
				t.Errorf("expected %d VMs, got %d", tt.wantCount, len(result))
			}
		})
	}
}

func TestFilterVMsOnKnownHypervisors_NilInputs(t *testing.T) {
	tests := []struct {
		name           string
		vms            []VM
		hypervisorList *hv1.HypervisorList
		wantCount      int
	}{
		{
			name: "nil hypervisor items does not panic",
			vms:  []VM{{UUID: "vm-1", CurrentHypervisor: "host1"}},
			hypervisorList: &hv1.HypervisorList{
				Items: nil,
			},
			wantCount: 0,
		},
		{
			name: "nil VMs does not panic",
			vms:  nil,
			hypervisorList: &hv1.HypervisorList{
				Items: []hv1.Hypervisor{
					{ObjectMeta: metav1.ObjectMeta{Name: "host1"}},
				},
			},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("filterVMsOnKnownHypervisors panicked: %v", r)
				}
			}()

			result := filterVMsOnKnownHypervisors(tt.vms, tt.hypervisorList)

			if len(result) != tt.wantCount {
				t.Errorf("expected %d VMs, got %d", tt.wantCount, len(result))
			}
		})
	}
}

// mockNovaReader implements NovaReader for testing.
type mockNovaReader struct {
	getAllServersFunc   func(ctx context.Context) ([]nova.Server, error)
	getAllFlavorsFunc   func(ctx context.Context) ([]nova.Flavor, error)
	getServerByIDFunc   func(ctx context.Context, serverID string) (*nova.Server, error)
	getFlavorByNameFunc func(ctx context.Context, flavorName string) (*nova.Flavor, error)
}

func (m *mockNovaReader) GetAllServers(ctx context.Context) ([]nova.Server, error) {
	if m.getAllServersFunc != nil {
		return m.getAllServersFunc(ctx)
	}
	return nil, nil
}

func (m *mockNovaReader) GetAllFlavors(ctx context.Context) ([]nova.Flavor, error) {
	if m.getAllFlavorsFunc != nil {
		return m.getAllFlavorsFunc(ctx)
	}
	return nil, nil
}

func (m *mockNovaReader) GetServerByID(ctx context.Context, serverID string) (*nova.Server, error) {
	if m.getServerByIDFunc != nil {
		return m.getServerByIDFunc(ctx, serverID)
	}
	return nil, nil
}

func (m *mockNovaReader) GetFlavorByName(ctx context.Context, flavorName string) (*nova.Flavor, error) {
	if m.getFlavorByNameFunc != nil {
		return m.getFlavorByNameFunc(ctx, flavorName)
	}
	return nil, nil
}

func (m *mockNovaReader) GetDeletedServerByID(_ context.Context, _ string) (*nova.DeletedServer, error) {
	return nil, nil
}
