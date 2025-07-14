// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
)

type mockCycleDetector struct {
	FilterFunc func(ctx context.Context, vmIDs []string) ([]string, error)
}

func (m *mockCycleDetector) Filter(ctx context.Context, vmIDs []string) ([]string, error) {
	if m.FilterFunc != nil {
		return m.FilterFunc(ctx, vmIDs)
	}
	return nil, nil
}

func TestCycleDetector_Filter(t *testing.T) {
	ctx := t.Context()
	novaAPI := &mockNovaAPI{}
	config := conf.DeschedulerConfig{}
	detector := NewCycleDetector(novaAPI, config)

	// Test with no VMs
	_, err := detector.Filter(ctx, []string{})
	if err != nil {
		t.Errorf("Filter returned an error: %v", err)
	}
}

func TestCycleDetector_FilterWithCycles(t *testing.T) {
	ctx := t.Context()
	novaAPI := &mockNovaAPI{
		GetServerMigrationsFunc: func(ctx context.Context, id string) ([]migration, error) {
			if id == "vm-123" {
				return []migration{
					{SourceCompute: "host1", DestCompute: "host2"},
					{SourceCompute: "host2", DestCompute: "host1"}, // Cycle here
				}, nil
			}
			if id == "vm-456" {
				return []migration{
					{SourceCompute: "host3", DestCompute: "host4"},
					{SourceCompute: "host4", DestCompute: "host5"},
					{SourceCompute: "host5", DestCompute: "host3"}, // Cycle here
				}, nil
			}
			return nil, nil
		},
	}
	config := conf.DeschedulerConfig{}
	detector := NewCycleDetector(novaAPI, config)

	// Test with VMs that have migration cycles
	result, err := detector.Filter(ctx, []string{"vm-123", "vm-456"})
	if err != nil {
		t.Errorf("Filter returned an error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("Expected no VMs to be returned due to cycles, got: %v", result)
	}
}
