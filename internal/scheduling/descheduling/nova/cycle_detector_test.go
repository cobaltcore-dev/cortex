// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/conf"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/descheduling/nova/plugins"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type mockCycleDetectorNovaAPI struct {
	migrations map[string][]migration
	getError   error
}

func (m *mockCycleDetectorNovaAPI) Init(ctx context.Context, client client.Client, conf conf.Config) error {
	return nil
}

func (m *mockCycleDetectorNovaAPI) Get(ctx context.Context, id string) (server, error) {
	return server{}, errors.New("not implemented")
}

func (m *mockCycleDetectorNovaAPI) LiveMigrate(ctx context.Context, id string) error {
	return errors.New("not implemented")
}

func (m *mockCycleDetectorNovaAPI) GetServerMigrations(ctx context.Context, id string) ([]migration, error) {
	if m.getError != nil {
		return nil, m.getError
	}
	if migs, ok := m.migrations[id]; ok {
		return migs, nil
	}
	return []migration{}, nil
}

func TestCycleDetector_Filter(t *testing.T) {
	tests := []struct {
		name       string
		decisions  []plugins.Decision
		migrations map[string][]migration
		expected   []plugins.Decision
		expectErr  bool
	}{
		{
			name: "no cycles - all decisions pass through",
			decisions: []plugins.Decision{
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
			expected: []plugins.Decision{
				{VMID: "vm-1", Reason: "high CPU", Host: "host-a"},
				{VMID: "vm-2", Reason: "high memory", Host: "host-b"},
			},
		},
		{
			name: "simple cycle detected - decision filtered out",
			decisions: []plugins.Decision{
				{VMID: "vm-1", Reason: "high CPU", Host: "host-a"},
			},
			migrations: map[string][]migration{
				"vm-1": {
					{SourceCompute: "host-a", DestCompute: "host-b"},
					{SourceCompute: "host-b", DestCompute: "host-a"}, // Cycle back to host-a
				},
			},
			expected: []plugins.Decision{}, // Filtered out due to cycle
		},
		{
			name: "three-hop cycle detected",
			decisions: []plugins.Decision{
				{VMID: "vm-1", Reason: "high CPU", Host: "host-a"},
			},
			migrations: map[string][]migration{
				"vm-1": {
					{SourceCompute: "host-a", DestCompute: "host-b"},
					{SourceCompute: "host-b", DestCompute: "host-c"},
					{SourceCompute: "host-c", DestCompute: "host-a"}, // Cycle back to host-a
				},
			},
			expected: []plugins.Decision{}, // Filtered out due to cycle
		},
		{
			name: "mixed scenarios - some cycles, some not",
			decisions: []plugins.Decision{
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
			expected: []plugins.Decision{
				{VMID: "vm-2", Reason: "high memory", Host: "host-x"},
				{VMID: "vm-3", Reason: "high disk", Host: "host-y"},
			},
		},
		{
			name: "complex cycle with multiple hops",
			decisions: []plugins.Decision{
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
			expected: []plugins.Decision{}, // Filtered out due to cycle
		},
		{
			name: "no migrations - decision passes through",
			decisions: []plugins.Decision{
				{VMID: "vm-1", Reason: "high CPU", Host: "host-a"},
			},
			migrations: map[string][]migration{
				"vm-1": {}, // No migrations
			},
			expected: []plugins.Decision{
				{VMID: "vm-1", Reason: "high CPU", Host: "host-a"},
			},
		},
		{
			name: "single migration - no cycle possible",
			decisions: []plugins.Decision{
				{VMID: "vm-1", Reason: "high CPU", Host: "host-a"},
			},
			migrations: map[string][]migration{
				"vm-1": {
					{SourceCompute: "host-a", DestCompute: "host-b"},
				},
			},
			expected: []plugins.Decision{
				{VMID: "vm-1", Reason: "high CPU", Host: "host-a"},
			},
		},
		{
			name: "API error when getting migrations",
			decisions: []plugins.Decision{
				{VMID: "vm-1", Reason: "high CPU", Host: "host-a"},
			},
			migrations: map[string][]migration{},
			expectErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAPI := &mockCycleDetectorNovaAPI{
				migrations: tt.migrations,
			}

			if tt.expectErr {
				mockAPI.getError = errors.New("API error")
			}

			detector := cycleDetector{novaAPI: mockAPI}

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
			expectedMap := make(map[string]plugins.Decision)
			for _, d := range tt.expected {
				expectedMap[d.VMID] = d
			}

			for _, resultDecision := range result {
				expectedDecision, found := expectedMap[resultDecision.VMID]
				if !found {
					t.Errorf("unexpected decision for VM %s", resultDecision.VMID)
					continue
				}

				if resultDecision.Reason != expectedDecision.Reason {
					t.Errorf("expected reason %s for VM %s, got %s",
						expectedDecision.Reason, resultDecision.VMID, resultDecision.Reason)
				}

				if resultDecision.Host != expectedDecision.Host {
					t.Errorf("expected host %s for VM %s, got %s",
						expectedDecision.Host, resultDecision.VMID, resultDecision.Host)
				}
			}
		})
	}
}

func TestCycleDetector_Filter_EmptyDecisions(t *testing.T) {
	mockAPI := &mockCycleDetectorNovaAPI{
		migrations: map[string][]migration{},
	}

	detector := cycleDetector{novaAPI: mockAPI}

	ctx := context.Background()
	result, err := detector.Filter(ctx, []plugins.Decision{})

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("expected empty result for empty input, got %d decisions", len(result))
	}
}
