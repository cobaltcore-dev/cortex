// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type mockExecutorNovaClient struct {
	servers        map[string]server
	migrations     map[string][]migration
	getError       error
	migrateError   error
	migrationDelay time.Duration
}

func (m *mockExecutorNovaClient) Init(ctx context.Context, client client.Client, conf conf.Config) error {
	return nil
}

func (m *mockExecutorNovaClient) Get(ctx context.Context, id string) (server, error) {
	if m.getError != nil {
		return server{}, m.getError
	}
	if s, ok := m.servers[id]; ok {
		return s, nil
	}
	return server{}, errors.New("server not found")
}

func (m *mockExecutorNovaClient) LiveMigrate(ctx context.Context, id string) error {
	if m.migrateError != nil {
		return m.migrateError
	}
	// Simulate migration by updating server status and host after delay
	if s, ok := m.servers[id]; ok {
		if m.migrationDelay > 0 {
			s.Status = "MIGRATING"
			m.servers[id] = s
			go func() {
				time.Sleep(m.migrationDelay)
				s.Status = "ACTIVE"
				s.ComputeHost = "new-host"
				m.servers[id] = s
			}()
		} else {
			s.ComputeHost = "new-host"
			m.servers[id] = s
		}
	}
	return nil
}

func (m *mockExecutorNovaClient) GetServerMigrations(ctx context.Context, id string) ([]migration, error) {
	if migs, ok := m.migrations[id]; ok {
		return migs, nil
	}
	return []migration{}, nil
}

// Create a zero-value Monitor for testing
func newMockMonitor() lib.DetectorMonitor[plugins.VMDetection] {
	return lib.DetectorMonitor[plugins.VMDetection]{}
}

func TestExecutor_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	err := v1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	tests := []struct {
		name               string
		descheduling       *v1alpha1.Descheduling
		novaAPI            *mockExecutorNovaClient
		config             conf.Config
		expectedReady      bool
		expectedInProgress bool
		expectedError      string
		expectDeletion     bool
	}{
		{
			name: "successful migration",
			descheduling: &v1alpha1.Descheduling{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-descheduling",
					Namespace: "default",
				},
				Spec: v1alpha1.DeschedulingSpec{
					RefType:      v1alpha1.DeschedulingSpecVMReferenceNovaServerUUID,
					Ref:          "vm-123",
					PrevHostType: v1alpha1.DeschedulingSpecHostTypeNovaComputeHostName,
					PrevHost:     "old-host",
				},
				Status: v1alpha1.DeschedulingStatus{},
			},
			novaAPI: &mockExecutorNovaClient{
				servers: map[string]server{
					"vm-123": {ID: "vm-123", Status: "ACTIVE", ComputeHost: "old-host"},
				},
			},
			config: conf.Config{
				DisableDeschedulerDryRun: true,
			},
			expectedReady:      true,
			expectedInProgress: false,
		},
		{
			name: "dry run mode",
			descheduling: &v1alpha1.Descheduling{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-descheduling",
					Namespace: "default",
				},
				Spec: v1alpha1.DeschedulingSpec{
					RefType:      v1alpha1.DeschedulingSpecVMReferenceNovaServerUUID,
					Ref:          "vm-123",
					PrevHostType: v1alpha1.DeschedulingSpecHostTypeNovaComputeHostName,
				},
				Status: v1alpha1.DeschedulingStatus{},
			},
			novaAPI: &mockExecutorNovaClient{
				servers: map[string]server{
					"vm-123": {ID: "vm-123", Status: "ACTIVE", ComputeHost: "old-host"},
				},
			},
			config: conf.Config{
				DisableDeschedulerDryRun: false,
			},
			expectedReady:      false,
			expectedInProgress: false,
		},
		{
			name: "unsupported ref type",
			descheduling: &v1alpha1.Descheduling{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-descheduling",
					Namespace: "default",
				},
				Spec: v1alpha1.DeschedulingSpec{
					RefType: "unsupported-type",
					Ref:     "vm-123",
				},
				Status: v1alpha1.DeschedulingStatus{},
			},
			novaAPI:            &mockExecutorNovaClient{},
			expectedReady:      false,
			expectedInProgress: false,
			expectedError:      "unsupported refType: unsupported-type",
		},
		{
			name: "unsupported host type",
			descheduling: &v1alpha1.Descheduling{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-descheduling",
					Namespace: "default",
				},
				Spec: v1alpha1.DeschedulingSpec{
					RefType:      v1alpha1.DeschedulingSpecVMReferenceNovaServerUUID,
					Ref:          "vm-123",
					PrevHostType: "unsupported-host-type",
				},
				Status: v1alpha1.DeschedulingStatus{},
			},
			novaAPI:            &mockExecutorNovaClient{},
			expectedReady:      false,
			expectedInProgress: false,
			expectedError:      "unsupported prevHostType: unsupported-host-type",
		},
		{
			name: "missing ref",
			descheduling: &v1alpha1.Descheduling{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-descheduling",
					Namespace: "default",
				},
				Spec: v1alpha1.DeschedulingSpec{
					RefType:      v1alpha1.DeschedulingSpecVMReferenceNovaServerUUID,
					Ref:          "",
					PrevHostType: v1alpha1.DeschedulingSpecHostTypeNovaComputeHostName,
				},
				Status: v1alpha1.DeschedulingStatus{},
			},
			novaAPI:            &mockExecutorNovaClient{},
			expectedReady:      false,
			expectedInProgress: false,
			expectedError:      "missing ref",
		},
		{
			name: "vm not found - should delete descheduling",
			descheduling: &v1alpha1.Descheduling{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-descheduling",
					Namespace: "default",
				},
				Spec: v1alpha1.DeschedulingSpec{
					RefType:      v1alpha1.DeschedulingSpecVMReferenceNovaServerUUID,
					Ref:          "vm-not-found",
					PrevHostType: v1alpha1.DeschedulingSpecHostTypeNovaComputeHostName,
				},
				Status: v1alpha1.DeschedulingStatus{},
			},
			novaAPI:        &mockExecutorNovaClient{servers: map[string]server{}},
			expectDeletion: true,
		},
		{
			name: "vm not on expected host",
			descheduling: &v1alpha1.Descheduling{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-descheduling",
					Namespace: "default",
				},
				Spec: v1alpha1.DeschedulingSpec{
					RefType:      v1alpha1.DeschedulingSpecVMReferenceNovaServerUUID,
					Ref:          "vm-123",
					PrevHostType: v1alpha1.DeschedulingSpecHostTypeNovaComputeHostName,
					PrevHost:     "expected-host",
				},
				Status: v1alpha1.DeschedulingStatus{},
			},
			novaAPI: &mockExecutorNovaClient{
				servers: map[string]server{
					"vm-123": {ID: "vm-123", Status: "ACTIVE", ComputeHost: "different-host"},
				},
			},
			expectedReady:      false,
			expectedInProgress: false,
			expectedError:      "VM not on expected host, expected: expected-host, actual: different-host",
		},
		{
			name: "vm not active",
			descheduling: &v1alpha1.Descheduling{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-descheduling",
					Namespace: "default",
				},
				Spec: v1alpha1.DeschedulingSpec{
					RefType:      v1alpha1.DeschedulingSpecVMReferenceNovaServerUUID,
					Ref:          "vm-123",
					PrevHostType: v1alpha1.DeschedulingSpecHostTypeNovaComputeHostName,
				},
				Status: v1alpha1.DeschedulingStatus{},
			},
			novaAPI: &mockExecutorNovaClient{
				servers: map[string]server{
					"vm-123": {ID: "vm-123", Status: "SHUTOFF", ComputeHost: "host1"},
				},
			},
			expectedReady:      false,
			expectedInProgress: false,
			expectedError:      "VM not active, current status: SHUTOFF",
		},
		{
			name: "migration fails",
			descheduling: &v1alpha1.Descheduling{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-descheduling",
					Namespace: "default",
				},
				Spec: v1alpha1.DeschedulingSpec{
					RefType:      v1alpha1.DeschedulingSpecVMReferenceNovaServerUUID,
					Ref:          "vm-123",
					PrevHostType: v1alpha1.DeschedulingSpecHostTypeNovaComputeHostName,
				},
				Status: v1alpha1.DeschedulingStatus{},
			},
			novaAPI: &mockExecutorNovaClient{
				servers: map[string]server{
					"vm-123": {ID: "vm-123", Status: "ACTIVE", ComputeHost: "host1"},
				},
				migrateError: errors.New("migration failed"),
			},
			config: conf.Config{
				DisableDeschedulerDryRun: true,
			},
			expectedReady:      false,
			expectedInProgress: false,
			expectedError:      "failed to live-migrate VM: migration failed",
		},
		{
			name: "skip already in progress",
			descheduling: &v1alpha1.Descheduling{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-descheduling",
					Namespace: "default",
				},
				Spec: v1alpha1.DeschedulingSpec{
					RefType:      v1alpha1.DeschedulingSpecVMReferenceNovaServerUUID,
					Ref:          "vm-123",
					PrevHostType: v1alpha1.DeschedulingSpecHostTypeNovaComputeHostName,
				},
				Status: v1alpha1.DeschedulingStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.DeschedulingConditionInProgress,
							Status: metav1.ConditionTrue,
							Reason: "MigrationInProgress",
						},
					},
				},
			},
			novaAPI:            &mockExecutorNovaClient{},
			expectDeletion:     false,
			expectedReady:      false,
			expectedInProgress: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client with the descheduling object
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.descheduling).
				WithStatusSubresource(&v1alpha1.Descheduling{}).
				Build()

			executor := &DeschedulingsExecutor{
				Client:     client,
				Scheme:     scheme,
				NovaClient: tt.novaAPI,
				Conf:       tt.config,
				Monitor:    newMockMonitor(),
			}

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.descheduling.Name,
					Namespace: tt.descheduling.Namespace,
				},
			}

			ctx := context.Background()
			_, err := executor.Reconcile(ctx, req)

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if tt.expectDeletion {
				// Check if descheduling was deleted
				var updated v1alpha1.Descheduling
				err = client.Get(ctx, req.NamespacedName, &updated)
				if err == nil {
					t.Error("expected descheduling to be deleted, but it still exists")
				}
				return
			}

			// Check final status
			var updated v1alpha1.Descheduling
			err = client.Get(ctx, req.NamespacedName, &updated)
			if err != nil {
				t.Fatalf("failed to get updated descheduling: %v", err)
			}

			readyCond := meta.FindStatusCondition(updated.Status.Conditions, v1alpha1.DeschedulingConditionReady)
			inProgressCond := meta.FindStatusCondition(updated.Status.Conditions, v1alpha1.DeschedulingConditionInProgress)

			if tt.expectedReady {
				if readyCond == nil || readyCond.Status != metav1.ConditionTrue {
					t.Errorf("expected descheduling to be ready, but it's not")
				}
			} else {
				if tt.expectedError != "" {
					if readyCond == nil || readyCond.Status != metav1.ConditionFalse || readyCond.Message != tt.expectedError {
						t.Errorf("expected descheduling error '%s', got '%v'", tt.expectedError, readyCond)
					}
				} else {
					if readyCond != nil && readyCond.Status == metav1.ConditionTrue {
						t.Errorf("expected descheduling not to be ready, but it is")
					}
				}
			}

			if tt.expectedInProgress {
				if inProgressCond == nil || inProgressCond.Status != metav1.ConditionTrue {
					t.Errorf("expected descheduling to be in progress, but it's not")
				}
			} else {
				if inProgressCond != nil && inProgressCond.Status == metav1.ConditionTrue {
					t.Errorf("expected descheduling not to be in progress, but it is")
				}
			}
		})
	}
}

func TestDeschedulingsExecutor_ReconcileNotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	err := v1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	executor := &DeschedulingsExecutor{
		Client:     client,
		Scheme:     scheme,
		NovaClient: &mockExecutorNovaClient{},
		Monitor:    newMockMonitor(),
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "nonexistent",
			Namespace: "default",
		},
	}

	ctx := context.Background()
	result, err := executor.Reconcile(ctx, req)

	if err != nil {
		t.Errorf("expected no error for not found resource, got: %v", err)
	}

	if result.RequeueAfter > 0 {
		t.Error("expected no requeue for not found resource")
	}
}
