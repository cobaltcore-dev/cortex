// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/descheduling/nova/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type mockCycleBreakerNovaAPI struct {
	migrations map[string][]migration
	getError   error
}

func (m *mockCycleBreakerNovaAPI) Init(ctx context.Context, client client.Client, conf conf.Config) error {
	return nil
}

func (m *mockCycleBreakerNovaAPI) Get(ctx context.Context, id string) (server, error) {
	return server{}, errors.New("not implemented")
}

func (m *mockCycleBreakerNovaAPI) LiveMigrate(ctx context.Context, id string) error {
	return errors.New("not implemented")
}

func (m *mockCycleBreakerNovaAPI) GetServerMigrations(ctx context.Context, id string) ([]migration, error) {
	if m.getError != nil {
		return nil, m.getError
	}
	if migs, ok := m.migrations[id]; ok {
		return migs, nil
	}
	return []migration{}, nil
}

func TestCycleBreaker_Filter(t *testing.T) {
	tests := []struct {
		name       string
		decisions  []plugins.VMDetection
		migrations map[string][]migration
		expected   []plugins.VMDetection
		expectErr  bool
	}{
		{
			name: "no cycles - all decisions pass through",
			decisions: []plugins.VMDetection{
				{VMID: "vm-1", Reason: "high CPU", Host: "host-a"},
				{VMID: "vm-2", Reason: "high memory", Host: "host-b"},
			},
			migrations: map[string][]migration{
				"vm-1": {
					{SourceCompute: "host-a", DestCompute: "host-b"},
				},
				"vm-2": {
					{SourceCompute: "host-b", DestCompute: "host-c"},
				},
			},
			expected: []plugins.VMDetection{
				{VMID: "vm-1", Reason: "high CPU", Host: "host-a"},
				{VMID: "vm-2", Reason: "high memory", Host: "host-b"},
			},
		},
		{
			name: "simple cycle detected - decision filtered out",
			decisions: []plugins.VMDetection{
				{VMID: "vm-1", Reason: "high CPU", Host: "host-a"},
			},
			migrations: map[string][]migration{
				"vm-1": {
					{SourceCompute: "host-a", DestCompute: "host-b"},
					{SourceCompute: "host-b", DestCompute: "host-a"}, // Cycle back to host-a
				},
			},
			expected: []plugins.VMDetection{}, // Filtered out due to cycle
		},
		{
			name: "three-hop cycle detected",
			decisions: []plugins.VMDetection{
				{VMID: "vm-1", Reason: "high CPU", Host: "host-a"},
			},
			migrations: map[string][]migration{
				"vm-1": {
					{SourceCompute: "host-a", DestCompute: "host-b"},
					{SourceCompute: "host-b", DestCompute: "host-c"},
					{SourceCompute: "host-c", DestCompute: "host-a"}, // Cycle back to host-a
				},
			},
			expected: []plugins.VMDetection{}, // Filtered out due to cycle
		},
		{
			name: "mixed scenarios - some cycles, some not",
			decisions: []plugins.VMDetection{
				{VMID: "vm-1", Reason: "high CPU", Host: "host-a"},    // Has cycle
				{VMID: "vm-2", Reason: "high memory", Host: "host-x"}, // No cycle
				{VMID: "vm-3", Reason: "high disk", Host: "host-y"},   // No migrations
			},
			migrations: map[string][]migration{
				"vm-1": {
					{SourceCompute: "host-a", DestCompute: "host-b"},
					{SourceCompute: "host-b", DestCompute: "host-a"}, // Cycle
				},
				"vm-2": {
					{SourceCompute: "host-x", DestCompute: "host-y"},
					{SourceCompute: "host-y", DestCompute: "host-z"}, // No cycle
				},
				"vm-3": {}, // No migrations
			},
			expected: []plugins.VMDetection{
				{VMID: "vm-2", Reason: "high memory", Host: "host-x"},
				{VMID: "vm-3", Reason: "high disk", Host: "host-y"},
			},
		},
		{
			name: "complex cycle with multiple hops",
			decisions: []plugins.VMDetection{
				{VMID: "vm-1", Reason: "high CPU", Host: "host-a"},
			},
			migrations: map[string][]migration{
				"vm-1": {
					{SourceCompute: "host-a", DestCompute: "host-b"},
					{SourceCompute: "host-b", DestCompute: "host-c"},
					{SourceCompute: "host-c", DestCompute: "host-d"},
					{SourceCompute: "host-d", DestCompute: "host-b"}, // Cycle to host-b (not host-a)
				},
			},
			expected: []plugins.VMDetection{}, // Filtered out due to cycle
		},
		{
			name: "no migrations - decision passes through",
			decisions: []plugins.VMDetection{
				{VMID: "vm-1", Reason: "high CPU", Host: "host-a"},
			},
			migrations: map[string][]migration{
				"vm-1": {}, // No migrations
			},
			expected: []plugins.VMDetection{
				{VMID: "vm-1", Reason: "high CPU", Host: "host-a"},
			},
		},
		{
			name: "single migration - no cycle possible",
			decisions: []plugins.VMDetection{
				{VMID: "vm-1", Reason: "high CPU", Host: "host-a"},
			},
			migrations: map[string][]migration{
				"vm-1": {
					{SourceCompute: "host-a", DestCompute: "host-b"},
				},
			},
			expected: []plugins.VMDetection{
				{VMID: "vm-1", Reason: "high CPU", Host: "host-a"},
			},
		},
		{
			name: "API error when getting migrations",
			decisions: []plugins.VMDetection{
				{VMID: "vm-1", Reason: "high CPU", Host: "host-a"},
			},
			migrations: map[string][]migration{},
			expectErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAPI := &mockCycleBreakerNovaAPI{
				migrations: tt.migrations,
			}

			if tt.expectErr {
				mockAPI.getError = errors.New("API error")
			}

			detector := cycleBreaker{novaAPI: mockAPI}

			ctx := context.Background()
			result, err := detector.Filter(ctx, tt.decisions)

			if tt.expectErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if len(result) != len(tt.expected) {
				t.Errorf("expected %d decisions, got %d", len(tt.expected), len(result))
				return
			}

			// Check if all expected decisions are present
			expectedMap := make(map[string]plugins.VMDetection)
			for _, d := range tt.expected {
				expectedMap[d.VMID] = d
			}

			for _, resultVMDetection := range result {
				expectedVMDetection, found := expectedMap[resultVMDetection.VMID]
				if !found {
					t.Errorf("unexpected decision for VM %s", resultVMDetection.VMID)
					continue
				}

				if resultVMDetection.Reason != expectedVMDetection.Reason {
					t.Errorf("expected reason %s for VM %s, got %s",
						expectedVMDetection.Reason, resultVMDetection.VMID, resultVMDetection.Reason)
				}

				if resultVMDetection.Host != expectedVMDetection.Host {
					t.Errorf("expected host %s for VM %s, got %s",
						expectedVMDetection.Host, resultVMDetection.VMID, resultVMDetection.Host)
				}
			}
		})
	}
}

func TestCycleBreaker_Filter_EmptyVMDetections(t *testing.T) {
	mockAPI := &mockCycleBreakerNovaAPI{
		migrations: map[string][]migration{},
	}

	detector := cycleBreaker{novaAPI: mockAPI}

	ctx := context.Background()
	result, err := detector.Filter(ctx, []plugins.VMDetection{})

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("expected empty result for empty input, got %d decisions", len(result))
	}
}
