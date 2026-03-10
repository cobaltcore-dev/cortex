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
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type VMwareBinpackStepOpts struct {
	// ResourceWeights allows configuring the weight for each resource type when
	// calculating the binpacking score. The score is a weighted average of the
	// node's resource utilizations after placing the VM.
	// If a resource is not specified, is ignored in the score calculation
	// (equivalent to a weight of 0).
	ResourceWeights map[corev1.ResourceName]float64 `json:"resourceWeights"`
}

// Validate the options to ensure they are correct before running the weigher.
func (o VMwareBinpackStepOpts) Validate() error {
	if len(o.ResourceWeights) == 0 {
		return errors.New("at least one resource weight must be specified")
	}
	supportedResources := []corev1.ResourceName{
		corev1.ResourceMemory,
		corev1.ResourceCPU,
	}
	for resourceName, value := range o.ResourceWeights {
		if !slices.Contains(supportedResources, resourceName) {
			return fmt.Errorf(
				"unsupported resource %s in ResourceWeights, supported resources are: %v",
				resourceName, supportedResources,
			)
		}
		// Value == 0 means the weight shouldn't be provided or the weigher
		// disabled in general.
		if value == 0 {
			return fmt.Errorf("resource weight for %s can't be zero, if you want to "+
				"disable this resource in the weigher, remove it or the weigher", resourceName)
		}
		// Value < 0 doesn't work since the division of the
		// weighted sum by the total weight will turn the score positive again,
		// which is likely not what the user intended when setting a negative
		// weight to invert the weigher's behavior.
		if value < 0 {
			return fmt.Errorf("resource weight for %s can't be negative. "+
				"use weigher.multiplier to invert this weighers behavior", resourceName)
		}
	}
	return nil
}

// This step implements a binpacking weigher for workloads on vmware hypervisors.
// It pulls the requested vm into the smallest gaps possible, to ensure
// other hosts with less allocation stay free for bigger vms.
// Explanation of the algorithm: https://volcano.sh/en/docs/plugins/#binpack
type VMwareBinpackStep struct {
	// Base weigher providing common functionality.
	lib.BaseWeigher[api.ExternalSchedulerRequest, VMwareBinpackStepOpts]
}

// Initialize the step and validate that all required knowledges are ready.
func (s *VMwareBinpackStep) Init(ctx context.Context, client client.Client, weigher v1alpha1.WeigherSpec) error {
	if err := s.BaseWeigher.Init(ctx, client, weigher); err != nil {
		return err
	}
	if err := s.CheckKnowledges(ctx,
		corev1.ObjectReference{Name: "host-utilization"},
	); err != nil {
		return err
	}
	return nil
}

// Run this weigher in the pipeline after filters have been executed.
func (s *VMwareBinpackStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)
	result.Statistics["binpack score"] = s.PrepareStats(request, "float")

	hostUtilizationKnowledge := &v1alpha1.Knowledge{}
	if err := s.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "host-utilization"},
		hostUtilizationKnowledge,
	); err != nil {
		return nil, err
	}
	hostUtilizations, err := v1alpha1.
		UnboxFeatureList[compute.HostUtilization](hostUtilizationKnowledge.Status.Raw)
	if err != nil {
		return nil, err
	}
	utilizationByHost := make(map[string]compute.HostUtilization, len(request.Hosts))
	for _, hostUtilization := range hostUtilizations {
		utilizationByHost[hostUtilization.ComputeHost] = hostUtilization
	}
	vmResources := s.calcVMResources(request)

	for host := range result.Activations {
		utilization, ok := utilizationByHost[host]
		if !ok {
			traceLog.Warn("no utilization for host, skipping", "host", host)
			continue
		}
		allocations := s.calcHostAllocation(utilization)
		capacities := s.calcHostCapacity(utilization)

		var totalWeightedUtilization, totalWeight float64

		for resourceName, weight := range s.Options.ResourceWeights {
			capacity, ok := capacities[resourceName]
			if !ok {
				traceLog.Warn("no capacity for resource on host, skipping resource in score calculation",
					"host", host, "resource", resourceName)
				continue
			}
			if capacity.IsZero() {
				continue
			}
			allocation, ok := allocations[resourceName]
			if !ok {
				traceLog.Warn("no allocation for resource on host, skipping resource in score calculation",
					"host", host, "resource", resourceName)
				continue
			}
			vmReq, ok := vmResources[resourceName]
			if !ok {
				traceLog.Warn("no resource request for vm, skipping",
					"resource", resourceName)
				continue
			}
			used := allocation.DeepCopy()
			used.Add(vmReq)
			utilization := used.AsApproximateFloat64() / capacity.AsApproximateFloat64()
			totalWeightedUtilization += utilization * weight
			totalWeight += weight
		}

		var score float64
		if totalWeight != 0 {
			score = totalWeightedUtilization / totalWeight // This can be > 1.0
		}
		result.Activations[host] = score
		result.Statistics["binpack score"].Hosts[host] = score
		traceLog.Info("calculated binpack score for host",
			"host", host, "score", score)
	}

	return result, nil
}

// calcHostCapacity calculates the total capacity of the host.
func (s *VMwareBinpackStep) calcHostCapacity(hostUtilization compute.HostUtilization) map[corev1.ResourceName]resource.Quantity {
	resources := make(map[corev1.ResourceName]resource.Quantity)
	capaMemoryBytes := int64(hostUtilization.TotalRAMAllocatableMB) * 1_000_000
	resources[corev1.ResourceMemory] = *resource.
		NewQuantity(capaMemoryBytes, resource.DecimalSI)
	capaCPU := int64(hostUtilization.TotalVCPUsAllocatable)
	resources[corev1.ResourceCPU] = *resource.
		NewQuantity(capaCPU, resource.DecimalSI)
	return resources
}

// calcHostAllocation calculates the total allocated resources on the host.
func (s *VMwareBinpackStep) calcHostAllocation(hostUtilization compute.HostUtilization) map[corev1.ResourceName]resource.Quantity {
	resources := make(map[corev1.ResourceName]resource.Quantity)
	resources[corev1.ResourceMemory] = *resource.
		NewQuantity(int64(hostUtilization.RAMUsedMB)*1_000_000, resource.DecimalSI)
	resources[corev1.ResourceCPU] = *resource.
		NewQuantity(int64(hostUtilization.VCPUsUsed), resource.DecimalSI)
	return resources
}

// calcVMResources calculates the total resource requests for the VM to be scheduled.
func (s *VMwareBinpackStep) calcVMResources(req api.ExternalSchedulerRequest) map[corev1.ResourceName]resource.Quantity {
	resources := make(map[corev1.ResourceName]resource.Quantity)
	resourcesMemBytes := int64(req.Spec.Data.Flavor.Data.MemoryMB * 1_000_000) //nolint:gosec // memory values are bounded by Nova
	resourcesMemBytes *= int64(req.Spec.Data.NumInstances)                     //nolint:gosec // instance count is bounded by Nova
	resources[corev1.ResourceMemory] = *resource.
		NewQuantity(resourcesMemBytes, resource.DecimalSI)
	resourcesCPU := int64(req.Spec.Data.Flavor.Data.VCPUs) //nolint:gosec // vCPU values are bounded by Nova
	resourcesCPU *= int64(req.Spec.Data.NumInstances)      //nolint:gosec // instance count is bounded by Nova
	resources[corev1.ResourceCPU] = *resource.
		NewQuantity(resourcesCPU, resource.DecimalSI)
	return resources
}

func init() {
	Index["vmware_binpack"] = func() NovaWeigher { return &VMwareBinpackStep{} }
}
