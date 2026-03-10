// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

type KVMPreferSmallerHostsStepOpts struct {
	// ResourceWeights allows configuring the weight for each resource type when
	// calculating the small host preference score. The score is a weighted average
	// of the normalized distances from the smallest capacity for each resource.
	// If a resource is not specified, it is ignored in the score calculation
	// (equivalent to a weight of 0).
	ResourceWeights map[corev1.ResourceName]float64 `json:"resourceWeights"`
}

// Validate the options to ensure they are correct before running the weigher.
func (o KVMPreferSmallerHostsStepOpts) Validate() error {
	if len(o.ResourceWeights) == 0 {
		return errors.New("at least one resource weight must be specified")
	}
	supportedResources := []corev1.ResourceName{
		corev1.ResourceMemory,
		corev1.ResourceCPU,
	}
	for resourceName, val := range o.ResourceWeights {
		if val < 0 {
			return errors.New("resource weights must be greater than or equal to zero")
		}
		if !slices.Contains(supportedResources, resourceName) {
			return fmt.Errorf(
				"unsupported resource %s in ResourceWeights, supported resources are: %v",
				resourceName, supportedResources,
			)
		}
	}
	return nil
}

// This step pulls virtual machines onto smaller hosts (by capacity). This ensures
// that larger hosts are not overly fragmented with small VMs, and can still
// accommodate larger VMs when they need to be scheduled.
type KVMPreferSmallerHostsStep struct {
	// Base weigher providing common functionality.
	lib.BaseWeigher[api.ExternalSchedulerRequest, KVMPreferSmallerHostsStepOpts]
}

// Run this weigher in the pipeline after filters have been executed.
func (s *KVMPreferSmallerHostsStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)
	result.Statistics["small host score"] = s.PrepareStats(request, "float")

	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(context.Background(), hvs); err != nil {
		traceLog.Error("failed to list hypervisors", "error", err)
		return nil, err
	}
	hvsByName := make(map[string]hv1.Hypervisor, len(hvs.Items))
	for _, hv := range hvs.Items {
		hvsByName[hv.Name] = hv
	}

	// Calculate smallest and largest capacity for each resource across active hosts
	smallest := make(map[corev1.ResourceName]*resource.Quantity)
	largest := make(map[corev1.ResourceName]*resource.Quantity)

	for resourceName := range s.Options.ResourceWeights {
		for _, hv := range hvs.Items {
			// We don't want to consider this host if it has been filtered out.
			if _, ok := result.Activations[hv.Name]; !ok {
				continue
			}
			capacity, ok := hv.Status.Capacity[resourceName.String()]
			if !ok {
				traceLog.Warn("hypervisor has no capacity for resource, skipping",
					"host", hv.Name, "resource", resourceName)
				continue
			}
			if smallest[resourceName] == nil || capacity.Cmp(*smallest[resourceName]) < 0 {
				smallest[resourceName] = &capacity
			}
			if largest[resourceName] == nil || capacity.Cmp(*largest[resourceName]) > 0 {
				largest[resourceName] = &capacity
			}
		}
	}

	for host := range result.Activations {
		hv, ok := hvsByName[host]
		if !ok {
			traceLog.Warn("no hv for host, skipping", "host", host)
			continue
		}

		var totalWeightedScore, totalWeight float64

		for resourceName, weight := range s.Options.ResourceWeights {
			capacity, ok := hv.Status.Capacity[resourceName.String()]
			if !ok {
				traceLog.Warn("hypervisor has no capacity for resource, skipping",
					"host", hv.Name, "resource", resourceName)
				continue
			}

			smallestCap := smallest[resourceName]
			largestCap := largest[resourceName]

			if smallestCap == nil || largestCap == nil {
				traceLog.Warn("no capacity range found for resource, skipping",
					"resource", resourceName)
				continue
			}

			// If all hosts have the same capacity for this resource, skip it
			if smallestCap.Cmp(*largestCap) == 0 {
				traceLog.Info("all hypervisors have the same capacity for resource, skipping",
					"resource", resourceName)
				continue
			}

			// The score is based on the normalized distance of the host's capacity
			// from the smallest and largest capacities of the remaining hosts.
			// Hosts with smaller capacities will have higher scores.
			// Score = 1 - (capacity - smallest) / (largest - smallest)
			resourceScore := 1 - (capacity.AsApproximateFloat64()-smallestCap.AsApproximateFloat64())/
				(largestCap.AsApproximateFloat64()-smallestCap.AsApproximateFloat64())

			totalWeightedScore += resourceScore * weight
			totalWeight += weight
		}

		var score float64
		if totalWeight != 0 {
			score = totalWeightedScore / totalWeight
		}
		result.Activations[host] = score
		result.Statistics["small host score"].Hosts[host] = score
		traceLog.Info("calculated small host score for host",
			"host", host, "score", score)
	}

	return result, nil
}

func init() {
	Index["kvm_prefer_smaller_hosts"] = func() NovaWeigher { return &KVMPreferSmallerHostsStep{} }
}
