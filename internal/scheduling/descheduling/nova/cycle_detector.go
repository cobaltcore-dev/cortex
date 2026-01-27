// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/descheduling/nova/plugins"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type cycleDetector struct {
	// Nova API to get needed information for cycle detection.
	novaAPI NovaAPI
}

func NewCycleDetector() lib.CycleDetector[plugins.VMDetection] {
	return &cycleDetector{novaAPI: NewNovaAPI()}
}

// Initialize the cycle detector.
func (c *cycleDetector) Init(ctx context.Context, client client.Client, conf conf.Config) error {
	return c.novaAPI.Init(ctx, client, conf)
}

func (c *cycleDetector) Filter(ctx context.Context, decisions []plugins.VMDetection) ([]plugins.VMDetection, error) {
	keep := make(map[string]struct{}, len(decisions))
	for _, decision := range decisions {
		// Get the migrations for the VM.
		migrations, err := c.novaAPI.GetServerMigrations(ctx, decision.VMID)
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
			keep[decision.VMID] = struct{}{}
		}
	}
	var output []plugins.VMDetection
	for _, decision := range decisions {
		if _, ok := keep[decision.VMID]; ok {
			output = append(output, decision)
		}
	}
	return output, nil
}
