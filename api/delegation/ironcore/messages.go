// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package ironcore

import (
	"log/slog"

	ironcorev1alpha1 "github.com/cobaltcore-dev/cortex/api/delegation/ironcore/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type MachinePipelineRequest struct {
	// The available machine pools.
	Pools []ironcorev1alpha1.MachinePool `json:"pools"`
}

func (r MachinePipelineRequest) GetSubjects() []string {
	hosts := make([]string, len(r.Pools))
	for i, host := range r.Pools {
		hosts[i] = host.Name
	}
	return hosts
}
func (r MachinePipelineRequest) GetWeights() map[string]float64 {
	weights := make(map[string]float64, len(r.Pools))
	for _, pool := range r.Pools {
		weights[pool.Name] = 0.0
	}
	return weights
}
func (r MachinePipelineRequest) GetTraceLogArgs() []slog.Attr {
	return []slog.Attr{}
}
func (r MachinePipelineRequest) FilterSubjects(includedSubjects map[string]float64) lib.PipelineRequest {
	filteredPools := make([]ironcorev1alpha1.MachinePool, 0, len(includedSubjects))
	for _, pool := range r.Pools {
		if _, exists := includedSubjects[pool.Name]; exists {
			filteredPools = append(filteredPools, pool)
		}
	}
	r.Pools = filteredPools
	return r
}
