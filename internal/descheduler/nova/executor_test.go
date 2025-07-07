// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
)

type mockExecutor struct{}

func (m *mockExecutor) Init(ctx context.Context, config conf.DeschedulerConfig) {}
func (m *mockExecutor) Deschedule(ctx context.Context, vmIDs []string) error {
	return nil
}

func TestExecutor_Init(t *testing.T) {
	ctx := t.Context()
	config := conf.DeschedulerConfig{
		Nova: conf.NovaDeschedulerConfig{
			DisableDryRun: false,
		},
	}
	executor := &mockExecutor{}
	executor.Init(ctx, config)

	// Should not panic or error out
}

func TestExecutor_Deschedule(t *testing.T) {
	ctx := t.Context()
	vmIDs := []string{"vm-123", "vm-456"}
	executor := &mockExecutor{}

	// This should not panic or error out
	err := executor.Deschedule(ctx, vmIDs)
	if err != nil {
		t.Errorf("Deschedule returned an error: %v", err)
	}
}

func TestExecutor_DescheduleVM(t *testing.T) {
	serverStates := []server{
		{ID: "vm-123", Status: "ACTIVE", ComputeHost: "host1"},
		{ID: "vm-123", Status: "MIGRATING", ComputeHost: "host1"},
		{ID: "vm-123", Status: "MIGRATING", ComputeHost: "host2"},
		{ID: "vm-123", Status: "ACTIVE", ComputeHost: "host2"},
	}
	var currentState = 0
	api := &mockNovaAPI{
		GetFunc: func(ctx context.Context, id string) (server, error) {
			if currentState < len(serverStates) {
				state := serverStates[currentState]
				currentState++
				return state, nil
			}
			return server{}, errors.New("no more states")
		},
		LiveMigrateFunc: func(ctx context.Context, id string) error {
			return nil // Simulate successful migration
		},
	}

	executor := &executor{
		novaAPI: api,
		config: conf.DeschedulerConfig{
			Nova: conf.NovaDeschedulerConfig{
				DisableDryRun: true,
			},
		},
	}

	ctx := t.Context()
	vmId := "vm-123"
	err := executor.Deschedule(ctx, []string{vmId})
	if err != nil {
		t.Errorf("Deschedule failed: %v", err)
	}
}

func TestExecutor_DescheduleVM_Error(t *testing.T) {
	api := &mockNovaAPI{
		GetFunc: func(ctx context.Context, id string) (server, error) {
			return server{}, errors.New("not found") // Simulate not found error
		},
	}

	executor := &executor{
		novaAPI: api,
		config: conf.DeschedulerConfig{
			Nova: conf.NovaDeschedulerConfig{
				DisableDryRun: true,
			},
		},
	}

	ctx := t.Context()
	vmId := "vm-123"
	err := executor.Deschedule(ctx, []string{vmId})
	if err == nil {
		t.Error("Expected error but got nil")
	} else {
		t.Logf("Deschedule failed as expected: %v", err)
	}
}
