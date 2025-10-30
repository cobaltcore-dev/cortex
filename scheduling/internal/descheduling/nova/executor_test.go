// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/conf"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type mockExecutorNovaAPI struct {
	servers        map[string]server
	migrations     map[string][]migration
	getError       error
	migrateError   error
	migrationDelay time.Duration
}

func (m *mockExecutorNovaAPI) Init(ctx context.Context) {}

func (m *mockExecutorNovaAPI) Get(ctx context.Context, id string) (server, error) {
	if m.getError != nil {
		return server{}, m.getError
	}
	if s, ok := m.servers[id]; ok {
		return s, nil
	}
	return server{}, errors.New("server not found")
}

func (m *mockExecutorNovaAPI) LiveMigrate(ctx context.Context, id string) error {
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

func (m *mockExecutorNovaAPI) GetServerMigrations(ctx context.Context, id string) ([]migration, error) {
	if migs, ok := m.migrations[id]; ok {
		return migs, nil
	}
	return []migration{}, nil
}

// Create a zero-value Monitor for testing
func newMockMonitor() Monitor {
	return Monitor{}
}

func TestExecutor_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	err := v1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	tests := []struct {
		name           string
		descheduling   *v1alpha1.Descheduling
		novaAPI        *mockExecutorNovaAPI
		config         conf.Config
		expectedPhase  v1alpha1.DeschedulingStatusPhase
		expectedError  string
		expectDeletion bool
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
				Status: v1alpha1.DeschedulingStatus{
					Phase: v1alpha1.DeschedulingStatusPhaseQueued,
				},
			},
			novaAPI: &mockExecutorNovaAPI{
				servers: map[string]server{
					"vm-123": {ID: "vm-123", Status: "ACTIVE", ComputeHost: "old-host"},
				},
			},
			config: conf.Config{
				DisableDeschedulerDryRun: true,
			},
			expectedPhase: v1alpha1.DeschedulingStatusPhaseCompleted,
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
				Status: v1alpha1.DeschedulingStatus{
					Phase: v1alpha1.DeschedulingStatusPhaseQueued,
				},
			},
			novaAPI: &mockExecutorNovaAPI{
				servers: map[string]server{
					"vm-123": {ID: "vm-123", Status: "ACTIVE", ComputeHost: "old-host"},
				},
			},
			config: conf.Config{
				DisableDeschedulerDryRun: false,
			},
			expectedPhase: v1alpha1.DeschedulingStatusPhaseQueued,
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
				Status: v1alpha1.DeschedulingStatus{
					Phase: v1alpha1.DeschedulingStatusPhaseQueued,
				},
			},
			novaAPI:       &mockExecutorNovaAPI{},
			expectedPhase: v1alpha1.DeschedulingStatusPhaseFailed,
			expectedError: "unsupported refType: unsupported-type",
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
				Status: v1alpha1.DeschedulingStatus{
					Phase: v1alpha1.DeschedulingStatusPhaseQueued,
				},
			},
			novaAPI:       &mockExecutorNovaAPI{},
			expectedPhase: v1alpha1.DeschedulingStatusPhaseFailed,
			expectedError: "unsupported prevHostType: unsupported-host-type",
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
				Status: v1alpha1.DeschedulingStatus{
					Phase: v1alpha1.DeschedulingStatusPhaseQueued,
				},
			},
			novaAPI:       &mockExecutorNovaAPI{},
			expectedPhase: v1alpha1.DeschedulingStatusPhaseFailed,
			expectedError: "missing ref",
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
				Status: v1alpha1.DeschedulingStatus{
					Phase: v1alpha1.DeschedulingStatusPhaseQueued,
				},
			},
			novaAPI:        &mockExecutorNovaAPI{servers: map[string]server{}},
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
				Status: v1alpha1.DeschedulingStatus{
					Phase: v1alpha1.DeschedulingStatusPhaseQueued,
				},
			},
			novaAPI: &mockExecutorNovaAPI{
				servers: map[string]server{
					"vm-123": {ID: "vm-123", Status: "ACTIVE", ComputeHost: "different-host"},
				},
			},
			expectedPhase: v1alpha1.DeschedulingStatusPhaseFailed,
			expectedError: "VM not on expected host, expected: expected-host, actual: different-host",
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
				Status: v1alpha1.DeschedulingStatus{
					Phase: v1alpha1.DeschedulingStatusPhaseQueued,
				},
			},
			novaAPI: &mockExecutorNovaAPI{
				servers: map[string]server{
					"vm-123": {ID: "vm-123", Status: "SHUTOFF", ComputeHost: "host1"},
				},
			},
			expectedPhase: v1alpha1.DeschedulingStatusPhaseFailed,
			expectedError: "VM not active, current status: SHUTOFF",
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
				Status: v1alpha1.DeschedulingStatus{
					Phase: v1alpha1.DeschedulingStatusPhaseQueued,
				},
			},
			novaAPI: &mockExecutorNovaAPI{
				servers: map[string]server{
					"vm-123": {ID: "vm-123", Status: "ACTIVE", ComputeHost: "host1"},
				},
				migrateError: errors.New("migration failed"),
			},
			config: conf.Config{
				DisableDeschedulerDryRun: true,
			},
			expectedPhase: v1alpha1.DeschedulingStatusPhaseFailed,
			expectedError: "failed to live-migrate VM: migration failed",
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
					Phase: v1alpha1.DeschedulingStatusPhaseInProgress,
				},
			},
			novaAPI:       &mockExecutorNovaAPI{},
			expectedPhase: v1alpha1.DeschedulingStatusPhaseInProgress,
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

			executor := &Executor{
				Client:  client,
				Scheme:  scheme,
				NovaAPI: tt.novaAPI,
				Conf:    tt.config,
				Monitor: newMockMonitor(),
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

			if updated.Status.Phase != tt.expectedPhase {
				t.Errorf("expected phase %s, got %s", tt.expectedPhase, updated.Status.Phase)
			}

			if tt.expectedError != "" && meta.IsStatusConditionFalse(updated.Status.Conditions, v1alpha1.DeschedulingConditionError) {
				t.Error("expected error condition to be true")
			}

			if tt.expectedPhase == v1alpha1.DeschedulingStatusPhaseCompleted {
				if updated.Status.NewHost == "" {
					t.Error("expected NewHost to be set after successful migration")
				}
				if updated.Status.NewHostType != v1alpha1.DeschedulingSpecHostTypeNovaComputeHostName {
					t.Errorf("expected NewHostType to be %s, got %s",
						v1alpha1.DeschedulingSpecHostTypeNovaComputeHostName,
						updated.Status.NewHostType)
				}
			}
		})
	}
}

func TestExecutor_ReconcileNotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	err := v1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	executor := &Executor{
		Client:  client,
		Scheme:  scheme,
		NovaAPI: &mockExecutorNovaAPI{},
		Monitor: newMockMonitor(),
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

func TestExecutor_SetupWithManager(t *testing.T) {
	// This test verifies that SetupWithManager can be called without errors
	// We can't easily test the full controller setup without a real manager
	executor := &Executor{}

	// This would typically be tested in integration tests with a real manager
	// For unit tests, we just verify the method exists and can be called
	// Test that SetupWithManager method exists by calling it with a nil manager
	// This will return an error, but confirms the method exists
	err := executor.SetupWithManager(nil)
	if err == nil {
		t.Error("SetupWithManager should return an error when called with nil manager")
	}
}
