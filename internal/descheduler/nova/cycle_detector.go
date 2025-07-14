// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"

	"github.com/cobaltcore-dev/cortex/internal/conf"
)

type CycleDetector interface {
	// Filter descheduling decisions to avoid cycles.
	Filter(ctx context.Context, vmIDs []string) ([]string, error)
}

type cycleDetector struct {
	// Nova API to get needed information for cycle detection.
	novaAPI NovaAPI
	// Configuration for the cycle detector.
	config conf.DeschedulerConfig
}

func NewCycleDetector(novaAPI NovaAPI, config conf.DeschedulerConfig) CycleDetector {
	return &cycleDetector{novaAPI: novaAPI, config: config}
}

func (c *cycleDetector) Filter(ctx context.Context, vmIDs []string) ([]string, error) {
	keep := make(map[string]struct{}, len(vmIDs))
	for _, id := range vmIDs {
		// Get the migrations for the VM.
		migrations, err := c.novaAPI.GetServerMigrations(ctx, id)
		if err != nil {
			return nil, err
		}
		// Check if there are cycles in the migrations.
		visited := make(map[string]struct{}, len(migrations))
		var cycleDetected = false
		for i, migration := range migrations {
			if i == 0 {
				visited[migration.SourceCompute] = struct{}{}
			}
			if _, ok := visited[migration.DestCompute]; ok {
				// If we have already visited the destination compute, we have a cycle.
				cycleDetected = true
				break
			}
			visited[migration.DestCompute] = struct{}{}
		}
		if !cycleDetected {
			// Keep the VM if there are no cycles.
			keep[id] = struct{}{}
		}
	}
	output := make([]string, 0, len(keep))
	for id := range keep {
		output = append(output, id)
	}
	return output, nil
}
