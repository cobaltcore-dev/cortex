// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"errors"
	"fmt"

	"github.com/sapcc/go-api-declarations/liquid"
	. "go.xyrillian.de/gg/option"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
)

// ErrCapacityNotReady is returned by CalculateCapacity when one or more FlavorGroupCapacity
// CRDs have Ready=False, indicating the controller's last probe cycle failed. Callers should
// return 503 Service Unavailable rather than serving potentially stale data.
var ErrCapacityNotReady = errors.New("one or more FlavorGroupCapacity CRDs are not ready")

// quantityValue returns the int64 value stored under key in a CRD resource map, or 0 if absent.
func quantityValue(m map[string]resource.Quantity, key string) int64 {
	if qty, ok := m[key]; ok {
		return qty.Value()
	}
	return 0
}

// smallestFlavorTotalSlots returns the empty-datacenter VM slot count of the group's smallest
// flavor — the installed slot capacity if the whole eligible host pool held only that flavor.
func smallestFlavorTotalSlots(crd *v1alpha1.FlavorGroupCapacity) int64 {
	for _, f := range crd.Status.Flavors {
		if f.FlavorName == crd.Status.SmallestFlavorName {
			return f.TotalCapacityVMSlots
		}
	}
	return 0
}

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
//   - Fixed-ratio groups (e.g. HANA): installed empty-datacenter capacity minus the failover
//     carve-out (TotalCapacity / smallest-flavor TotalCapacityVMSlots, less the failover slice).
//   - Variable-ratio groups: RunningSlots + ExclusivelyFreeCapacity + ExclusivelyCommittedReserved,
//     with the _ram raw byte path preferred when available. Failover reservations are excluded.
//   - Usage: always None. Limes derives project usage from the separate Report-Usage endpoint, so
//     a usage value here is unused and would only invite misinterpretation of reserved capacity.
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

	for groupName, groupData := range flavorGroups {
		resCfg := c.conf.ResourceConfigForGroup(groupName)
		// Skip groups not configured for capacity reporting.
		if !resCfg.RAM.HasCapacity && !resCfg.Cores.HasCapacity && !resCfg.Instances.HasCapacity {
			continue
		}

		ramUnitBytes := int64(resCfg.RAM.RAMUnitMiB()) * 1024 * 1024 //nolint:gosec
		memKey := string(v1alpha1.CommittedResourceTypeMemory)
		coresKey := string(v1alpha1.CommittedResourceTypeCores)

		ramAZCapacity := make(map[liquid.AvailabilityZone]*liquid.AZResourceCapacityReport, len(req.AllAZs))
		coresAZCapacity := make(map[liquid.AvailabilityZone]*liquid.AZResourceCapacityReport, len(req.AllAZs))
		instancesAZCapacity := make(map[liquid.AvailabilityZone]*liquid.AZResourceCapacityReport, len(req.AllAZs))

		for _, az := range req.AllAZs {
			crd, ok := crdByKey[groupAZKey{groupName, string(az)}]
			if !ok {
				zero := &liquid.AZResourceCapacityReport{Capacity: 0}
				ramAZCapacity[az] = zero
				coresAZCapacity[az] = &liquid.AZResourceCapacityReport{Capacity: 0}
				instancesAZCapacity[az] = &liquid.AZResourceCapacityReport{Capacity: 0}
				continue
			}

			if !apimeta.IsStatusConditionTrue(crd.Status.Conditions, v1alpha1.FlavorGroupCapacityConditionReady) {
				return liquid.ServiceCapacityReport{}, fmt.Errorf("%w: flavorGroup=%s az=%s", ErrCapacityNotReady, groupName, string(az))
			}

			// ExclusivelyFreeSlots is pre-computed by the controller using min(memSlots, cpuSlots).
			exclusiveFreeSlots := uint64(crd.Status.ExclusivelyFreeSlots) //nolint:gosec

			// Reservation-blocked capacity (neither running nor free) is split into two disjoint
			// buckets by the controller. Committed-resource reservations hold installed hardware for
			// a customer and stay in reported capacity; failover reservations hold hardware out of
			// service for host evacuation and are excluded.
			committedReservedSlots := uint64(crd.Status.ExclusivelyCommittedReservedSlots) //nolint:gosec
			committedReservedCores := quantityValue(crd.Status.ExclusivelyCommittedReservedCapacity, coresKey)
			committedReservedMemBytes := quantityValue(crd.Status.ExclusivelyCommittedReservedCapacity, memKey)
			failoverMemBytes := quantityValue(crd.Status.ExclusivelyFailoverReservedCapacity, memKey)

			// Instances capacity.
			// Fixed-ratio groups (e.g. HANA) report installed empty-datacenter slots minus the
			// failover carve-out. The running + free + committed summation underreports here because
			// the round-robin free split drops fully-occupied hosts, so capacity falls below installed
			// hardware. Empty-datacenter slots already include running, free and committed-reserved
			// hosts alike, so we only subtract the failover slice to exclude evacuation hold-back.
			// Variable-ratio groups keep the summation: RunningSlots (not RunningInstances) so a VM
			// larger than the smallest flavor counts as the several slots it occupies.
			var instancesCapacity uint64
			if groupData.HasFixedRamCoreRatio() {
				totalSlots := smallestFlavorTotalSlots(crd)
				instancesCapacity = uint64(max(totalSlots-crd.Status.ExclusivelyFailoverReservedSlots, 0))
			} else {
				runningSlots := uint64(crd.Status.RunningSlots) //nolint:gosec
				instancesCapacity = runningSlots + exclusiveFreeSlots + committedReservedSlots
			}

			// RAM capacity in declared units. Fixed-ratio groups report in slots (1 unit = 1 instance).
			var ramCapacity uint64
			if groupData.HasFixedRamCoreRatio() {
				ramCapacity = instancesCapacity
			} else if ramUnitBytes > 0 {
				// Variable-ratio: prefer raw hardware bytes (not slot-quantized), which for
				// CPU-bound groups avoids the smallest-flavor quantum severely undercounting memory.
				// ExclusivelyRawCapacity already includes reserved-but-empty hosts, so subtract only
				// the failover slice to exclude it while keeping committed reserved capacity.
				if raw := quantityValue(crd.Status.ExclusivelyRawCapacity, memKey); raw > 0 {
					ramCapacity = uint64(max(raw-failoverMemBytes, 0)) / uint64(ramUnitBytes)
				} else {
					runningMemBytes := quantityValue(crd.Status.RunningResources, memKey)
					freeMemBytes := quantityValue(crd.Status.ExclusivelyFreeCapacity, memKey)
					ramCapacity = uint64(runningMemBytes+freeMemBytes+committedReservedMemBytes) / uint64(ramUnitBytes) //nolint:gosec
				}
			}

			// Cores capacity. Fixed-ratio groups use installed empty-datacenter cores minus the
			// failover carve-out, for the same dropout reason as instances. Variable-ratio groups sum
			// running + exclusively free + committed reserved cores.
			var coresCapacity uint64
			if groupData.HasFixedRamCoreRatio() {
				totalCores := quantityValue(crd.Status.TotalCapacity, coresKey)
				failoverCores := quantityValue(crd.Status.ExclusivelyFailoverReservedCapacity, coresKey)
				coresCapacity = uint64(max(totalCores-failoverCores, 0))
			} else {
				runningCoresCount := quantityValue(crd.Status.RunningResources, coresKey)
				freeCoresCount := quantityValue(crd.Status.ExclusivelyFreeCapacity, coresKey)
				coresCapacity = uint64(runningCoresCount + freeCoresCount + committedReservedCores) //nolint:gosec
			}

			// Usage is intentionally None: Limes derives project usage from the separate
			// Report-Usage endpoint, so a usage value here is unused and would only invite
			// misinterpretation of reserved-vs-free capacity.
			ramEntry := &liquid.AZResourceCapacityReport{Capacity: ramCapacity, Usage: None[uint64]()}
			coresEntry := &liquid.AZResourceCapacityReport{Capacity: coresCapacity, Usage: None[uint64]()}
			instancesEntry := &liquid.AZResourceCapacityReport{Capacity: instancesCapacity, Usage: None[uint64]()}

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
