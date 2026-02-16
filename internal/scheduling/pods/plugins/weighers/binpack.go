// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"errors"
	"log/slog"
	"math"

	api "github.com/cobaltcore-dev/cortex/api/external/pods"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/pods/helpers"
	corev1 "k8s.io/api/core/v1"
)

type BinpackingStepOpts struct {
	ResourceWeights map[corev1.ResourceName]float64 `json:"resourceWeights"`
}

func (o BinpackingStepOpts) Validate() error {
	for _, val := range o.ResourceWeights {
		if val < 0 {
			return errors.New("resource weights must be greater than zero")
		}
	}
	return nil
}

type BinpackingStep struct {
	lib.BaseWeigher[api.PodPipelineRequest, BinpackingStepOpts]
}

func (s *BinpackingStep) Run(traceLog *slog.Logger, request api.PodPipelineRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)

	podResources := helpers.GetPodResourceRequests(request.Pod)

	for _, node := range request.Nodes {
		result.Activations[node.Name] = s.calculateBinpackScore(node, podResources, s.Options.ResourceWeights)
	}

	return result, nil
}

// calculateBinpackScore outputs a weighted average of the
// node's resource utilizations after the pod has been places.
func (s *BinpackingStep) calculateBinpackScore(node corev1.Node, podResources corev1.ResourceList, weights map[corev1.ResourceName]float64) float64 {
	if node.Status.Capacity == nil || node.Status.Allocatable == nil {
		return 0.0
	}

	var totalWeightedUtilization, totalWeight float64

	for resource, qty := range podResources {
		capacity := node.Status.Capacity[resource]
		allocatable := node.Status.Allocatable[resource]
		weight, ok := weights[resource]
		if !ok {
			weight = 1.0
		}

		if capacity.IsZero() {
			continue
		}

		used := capacity.DeepCopy()
		used.Sub(allocatable)
		used.Add(qty)

		utilization := used.AsApproximateFloat64() / capacity.AsApproximateFloat64()
		totalWeightedUtilization += utilization * weight
		totalWeight += weight
	}

	if totalWeight == 0 {
		return 0.0
	}

	return math.Min(1.0, totalWeightedUtilization/totalWeight)
}

func init() {
	Index["binpack"] = func() PodWeigher { return &BinpackingStep{} }
}
