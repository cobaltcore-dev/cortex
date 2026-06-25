// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"

	"github.com/sapcc/go-api-declarations/liquid"
	. "go.xyrillian.de/gg/option"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
)

// CapacityCalculator computes capacity reports for Limes LIQUID API.
type CapacityCalculator struct {
	client client.Client
	conf   APIConfig
}

func NewCapacityCalculator(client client.Client, conf APIConfig) *CapacityCalculator {
	return &CapacityCalculator{client: client, conf: conf}
}

// CalculateCapacity computes per-AZ capacity for all flavor groups.
// For each flavor group, three resources are reported: _ram, _cores, _instances.
// All values are read from FlavorGroupCapacity CRDs pre-computed by the capacity controller:
//   - Capacity: RunningInstances + ExclusivelyFreeCapacity converted to slots.
//   - Usage: RunningInstances / RunningResources.
func (c *CapacityCalculator) CalculateCapacity(ctx context.Context, req liquid.ServiceCapacityRequest) (liquid.ServiceCapacityReport, error) {
	knowledge := &reservations.FlavorGroupKnowledgeClient{Client: c.client}
	flavorGroups, err := knowledge.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		return liquid.ServiceCapacityReport{}, fmt.Errorf("failed to get flavor groups: %w", err)
	}

	var infoVersion int64 = -1
	if knowledgeCRD, err := knowledge.Get(ctx); err == nil && knowledgeCRD != nil && !knowledgeCRD.Status.LastContentChange.IsZero() {
		infoVersion = knowledgeCRD.Status.LastContentChange.Unix()
	}

	var capacityList v1alpha1.FlavorGroupCapacityList
	if err := c.client.List(ctx, &capacityList); err != nil {
		return liquid.ServiceCapacityReport{}, fmt.Errorf("failed to list FlavorGroupCapacity CRDs: %w", err)
	}
	type groupAZKey struct{ group, az string }
	crdByKey := make(map[groupAZKey]*v1alpha1.FlavorGroupCapacity, len(capacityList.Items))
	for i := range capacityList.Items {
		crd := &capacityList.Items[i]
		crdByKey[groupAZKey{crd.Spec.FlavorGroup, crd.Spec.AvailabilityZone}] = crd
	}

	report := liquid.ServiceCapacityReport{
		InfoVersion: infoVersion,
		Resources:   make(map[liquid.ResourceName]*liquid.ResourceCapacityReport),
	}

	logger := LoggerFromContext(ctx)
	for groupName, groupData := range flavorGroups {
		resCfg := c.conf.ResourceConfigForGroup(groupName)
		// Skip groups not configured for capacity reporting.
		if !resCfg.RAM.HasCapacity && !resCfg.Cores.HasCapacity && !resCfg.Instances.HasCapacity {
			continue
		}

		ramUnitBytes := int64(resCfg.RAM.RAMUnitMiB()) * 1024 * 1024 //nolint:gosec

		ramAZCapacity := make(map[liquid.AvailabilityZone]*liquid.AZResourceCapacityReport, len(req.AllAZs))
		coresAZCapacity := make(map[liquid.AvailabilityZone]*liquid.AZResourceCapacityReport, len(req.AllAZs))
		instancesAZCapacity := make(map[liquid.AvailabilityZone]*liquid.AZResourceCapacityReport, len(req.AllAZs))

		for _, az := range req.AllAZs {
			crd, ok := crdByKey[groupAZKey{groupName, string(az)}]
			if !ok {
				// No CRD for this (group, AZ) pair — report zero.
				zero := &liquid.AZResourceCapacityReport{Capacity: 0}
				ramAZCapacity[az] = zero
				coresAZCapacity[az] = &liquid.AZResourceCapacityReport{Capacity: 0}
				instancesAZCapacity[az] = &liquid.AZResourceCapacityReport{Capacity: 0}
				continue
			}

			if !apimeta.IsStatusConditionTrue(crd.Status.Conditions, v1alpha1.FlavorGroupCapacityConditionReady) {
				logger.Info("FlavorGroupCapacity CRD is stale, reporting capacity without usage",
					"flavorGroup", groupName, "az", az)
			}

			// ExclusivelyFreeSlots is pre-computed by the controller using min(memSlots, cpuSlots).
			exclusiveFreeSlots := uint64(crd.Status.ExclusivelyFreeSlots) //nolint:gosec

			// Capacity = running + exclusively free, all derived from CRD bytes.
			runningInstances := uint64(crd.Status.RunningInstances) //nolint:gosec
			instancesCapacity := runningInstances + exclusiveFreeSlots

			// RAM capacity: running bytes + exclusively free bytes → declared units.
			// Fixed-ratio groups report in slots (1 unit = 1 instance).
			var ramCapacity uint64
			if groupData.HasFixedRamCoreRatio() {
				ramCapacity = instancesCapacity
			} else if ramUnitBytes > 0 {
				runningMemBytes := int64(0)
				if qty, ok := crd.Status.RunningResources[string(v1alpha1.CommittedResourceTypeMemory)]; ok {
					runningMemBytes = qty.Value()
				}
				freeMemBytes := int64(0)
				if qty, ok := crd.Status.ExclusivelyFreeCapacity[string(v1alpha1.CommittedResourceTypeMemory)]; ok {
					freeMemBytes = qty.Value()
				}
				ramCapacity = uint64(runningMemBytes+freeMemBytes) / uint64(ramUnitBytes)
			}

			// Cores capacity: running cores + exclusively free cores.
			var coresCapacity uint64
			runningCoresCount := int64(0)
			if qty, ok := crd.Status.RunningResources[string(v1alpha1.CommittedResourceTypeCores)]; ok {
				runningCoresCount = qty.Value()
			}
			freeCoresCount := int64(0)
			if qty, ok := crd.Status.ExclusivelyFreeCapacity[string(v1alpha1.CommittedResourceTypeCores)]; ok {
				freeCoresCount = qty.Value()
			}
			coresCapacity = uint64(runningCoresCount + freeCoresCount)

			ramEntry := &liquid.AZResourceCapacityReport{Capacity: ramCapacity}
			coresEntry := &liquid.AZResourceCapacityReport{Capacity: coresCapacity}
			instancesEntry := &liquid.AZResourceCapacityReport{Capacity: instancesCapacity}

			// Usage from actual running VMs — only when CRD data is fresh.
			if apimeta.IsStatusConditionTrue(crd.Status.Conditions, v1alpha1.FlavorGroupCapacityConditionReady) {
				instancesEntry.Usage = Some[uint64](runningInstances)
				coresEntry.Usage = Some[uint64](uint64(runningCoresCount))

				if groupData.HasFixedRamCoreRatio() {
					ramEntry.Usage = Some[uint64](runningInstances)
				} else if ramUnitBytes > 0 {
					runningMemBytes := int64(0)
					if qty, ok := crd.Status.RunningResources[string(v1alpha1.CommittedResourceTypeMemory)]; ok {
						runningMemBytes = qty.Value()
					}
					ramEntry.Usage = Some[uint64](uint64(runningMemBytes) / uint64(ramUnitBytes))
				}
			}

			ramAZCapacity[az] = ramEntry
			coresAZCapacity[az] = coresEntry
			instancesAZCapacity[az] = instancesEntry
		}

		if resCfg.RAM.HasCapacity {
			report.Resources[liquid.ResourceName(ResourceNameRAM(groupName))] = &liquid.ResourceCapacityReport{
				PerAZ: ramAZCapacity,
			}
		}
		if resCfg.Cores.HasCapacity {
			report.Resources[liquid.ResourceName(ResourceNameCores(groupName))] = &liquid.ResourceCapacityReport{
				PerAZ: coresAZCapacity,
			}
		}
		if resCfg.Instances.HasCapacity {
			report.Resources[liquid.ResourceName(ResourceNameInstances(groupName))] = &liquid.ResourceCapacityReport{
				PerAZ: instancesAZCapacity,
			}
		}
	}

	return report, nil
}
