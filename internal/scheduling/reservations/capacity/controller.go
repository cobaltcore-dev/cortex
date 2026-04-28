// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package capacity

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
)

var log = ctrl.Log.WithName("capacity-controller").WithValues("module", "capacity")

// Controller reconciles FlavorGroupCapacity CRDs on a fixed interval.
// For each (flavor group × AZ) pair it runs two scheduler probes and updates the CRD status.
type Controller struct {
	client          client.Client
	schedulerClient *reservations.SchedulerClient
	config          Config
}

func NewController(c client.Client, config Config) *Controller {
	return &Controller{
		client:          c,
		schedulerClient: reservations.NewSchedulerClient(config.SchedulerURL),
		config:          config,
	}
}

// Start runs the periodic reconcile loop. Implements manager.Runnable.
func (c *Controller) Start(ctx context.Context) error {
	timer := time.NewTimer(0) // fire immediately on start
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			if err := c.reconcileAll(ctx); err != nil {
				log.Error(err, "reconcile cycle failed")
			}
			timer.Reset(c.config.ReconcileInterval)
		}
	}
}

// reconcileAll iterates all flavor groups × AZs and upserts FlavorGroupCapacity CRDs.
func (c *Controller) reconcileAll(ctx context.Context) error {
	knowledge := &reservations.FlavorGroupKnowledgeClient{Client: c.client}
	flavorGroups, err := knowledge.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to get flavor groups: %w", err)
	}

	var hvList hv1.HypervisorList
	if err := c.client.List(ctx, &hvList); err != nil {
		return fmt.Errorf("failed to list hypervisors: %w", err)
	}

	hvByName := make(map[string]hv1.Hypervisor, len(hvList.Items))
	for _, hv := range hvList.Items {
		hvByName[hv.Name] = hv
	}

	azs := availabilityZones(hvList.Items)

	for groupName, groupData := range flavorGroups {
		for _, az := range azs {
			if err := c.reconcileOne(ctx, groupName, groupData, az, hvByName, hvList.Items); err != nil {
				log.Error(err, "failed to reconcile flavor group capacity",
					"flavorGroup", groupName, "az", az)
				// Continue with other pairs rather than aborting the whole cycle.
			}
		}
	}
	return nil
}

// reconcileOne updates the FlavorGroupCapacity CRD for one (group × AZ) pair.
func (c *Controller) reconcileOne(
	ctx context.Context,
	groupName string,
	groupData compute.FlavorGroupFeature,
	az string,
	hvByName map[string]hv1.Hypervisor,
	allHVs []hv1.Hypervisor,
) error {
	smallestFlavor := groupData.SmallestFlavor
	smallestFlavorBytes := int64(smallestFlavor.MemoryMB) * 1024 * 1024 //nolint:gosec
	if smallestFlavorBytes <= 0 {
		return fmt.Errorf("smallest flavor %q has invalid memory %d MB", smallestFlavor.Name, smallestFlavor.MemoryMB)
	}

	// Empty-state probe: scheduler ignores all current VM allocations.
	totalCapacity, totalHosts, totalErr := c.probeScheduler(ctx, smallestFlavor, az, c.config.TotalPipeline, hvByName, smallestFlavorBytes)

	// Current-state probe: scheduler considers current VM allocations.
	totalPlaceable, placeableHosts, placeableErr := c.probeScheduler(ctx, smallestFlavor, az, c.config.PlaceablePipeline, hvByName, smallestFlavorBytes)

	// Count total instances on hypervisors in this AZ.
	totalInstances := countInstancesInAZ(allHVs, az)

	committedCapacity, committedErr := c.sumCommittedCapacity(ctx, groupName, az, smallestFlavorBytes)
	if committedErr != nil {
		log.Error(committedErr, "failed to sum committed capacity", "flavorGroup", groupName, "az", az)
		committedCapacity = 0
	}

	crdName := crdNameFor(groupName, az)
	fresh := totalErr == nil && placeableErr == nil

	var existing v1alpha1.FlavorGroupCapacity
	err := c.client.Get(ctx, types.NamespacedName{Name: crdName}, &existing)
	if apierrors.IsNotFound(err) {
		existing = v1alpha1.FlavorGroupCapacity{
			ObjectMeta: metav1.ObjectMeta{Name: crdName},
			Spec: v1alpha1.FlavorGroupCapacitySpec{
				FlavorGroup:      groupName,
				AvailabilityZone: az,
			},
		}
		if createErr := c.client.Create(ctx, &existing); createErr != nil {
			return fmt.Errorf("failed to create FlavorGroupCapacity %s: %w", crdName, createErr)
		}
	} else if err != nil {
		return fmt.Errorf("failed to get FlavorGroupCapacity %s: %w", crdName, err)
	}

	patch := client.MergeFrom(existing.DeepCopy())
	existing.Status.TotalCapacity = totalCapacity
	existing.Status.TotalHosts = totalHosts
	existing.Status.TotalPlaceable = totalPlaceable
	existing.Status.PlaceableHosts = placeableHosts
	existing.Status.TotalInstances = totalInstances
	existing.Status.CommittedCapacity = committedCapacity
	existing.Status.LastReconcileAt = metav1.Now()

	freshCondition := metav1.Condition{
		Type:               v1alpha1.FlavorGroupCapacityConditionReady,
		ObservedGeneration: existing.Generation,
	}
	if fresh {
		freshCondition.Status = metav1.ConditionTrue
		freshCondition.Reason = "ReconcileSucceeded"
		freshCondition.Message = "capacity data is up-to-date"
	} else {
		freshCondition.Status = metav1.ConditionFalse
		freshCondition.Reason = "ReconcileFailed"
		if totalErr != nil {
			freshCondition.Message = fmt.Sprintf("empty-state probe failed: %v", totalErr)
		} else {
			freshCondition.Message = fmt.Sprintf("current-state probe failed: %v", placeableErr)
		}
	}
	meta.SetStatusCondition(&existing.Status.Conditions, freshCondition)

	if patchErr := c.client.Status().Patch(ctx, &existing, patch); patchErr != nil {
		return fmt.Errorf("failed to patch FlavorGroupCapacity %s status: %w", crdName, patchErr)
	}
	return nil
}

// probeScheduler calls the scheduler with the given pipeline and returns capacity + host count.
func (c *Controller) probeScheduler(
	ctx context.Context,
	flavor compute.FlavorInGroup,
	az, pipeline string,
	hvByName map[string]hv1.Hypervisor,
	smallestFlavorBytes int64,
) (capacity, hosts int64, err error) {
	resp, err := c.schedulerClient.ScheduleReservation(ctx, reservations.ScheduleReservationRequest{
		InstanceUUID:     uuid.New().String(),
		ProjectID:        "cortex-capacity-probe",
		FlavorName:       flavor.Name,
		MemoryMB:         flavor.MemoryMB,
		VCPUs:            flavor.VCPUs,
		FlavorExtraSpecs: flavor.ExtraSpecs,
		AvailabilityZone: az,
		Pipeline:         pipeline,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("scheduler call failed (pipeline=%s): %w", pipeline, err)
	}

	hosts = int64(len(resp.Hosts)) //nolint:gosec
	for _, hostName := range resp.Hosts {
		hv, ok := hvByName[hostName]
		if !ok {
			continue
		}
		effectiveCap := hv.Status.EffectiveCapacity
		if effectiveCap == nil {
			effectiveCap = hv.Status.Capacity
		}
		if effectiveCap == nil {
			continue
		}
		memCap, ok := effectiveCap[hv1.ResourceMemory]
		if !ok {
			continue
		}
		if capBytes := memCap.Value(); capBytes > 0 {
			capacity += capBytes / smallestFlavorBytes
		}
	}
	return capacity, hosts, nil
}

// sumCommittedCapacity sums AcceptedAmount (or Spec.Amount as fallback) across all
// CommittedResource CRDs for the given (flavorGroup, az) pair with an active state
// (guaranteed or confirmed) and resource type memory. Returns the total in slots.
func (c *Controller) sumCommittedCapacity(ctx context.Context, groupName, az string, smallestFlavorBytes int64) (int64, error) {
	var list v1alpha1.CommittedResourceList
	if err := c.client.List(ctx, &list); err != nil {
		return 0, fmt.Errorf("failed to list CommittedResources: %w", err)
	}

	var total int64
	for _, cr := range list.Items {
		if cr.Spec.FlavorGroupName != groupName {
			continue
		}
		if cr.Spec.AvailabilityZone != az {
			continue
		}
		if cr.Spec.ResourceType != v1alpha1.CommittedResourceTypeMemory {
			continue
		}
		if cr.Spec.State != v1alpha1.CommitmentStatusGuaranteed && cr.Spec.State != v1alpha1.CommitmentStatusConfirmed {
			continue
		}
		amount := cr.Spec.Amount
		if cr.Status.AcceptedAmount != nil {
			amount = *cr.Status.AcceptedAmount
		}
		if bytes := amount.Value(); bytes > 0 {
			total += bytes / smallestFlavorBytes
		}
	}
	return total, nil
}

// availabilityZones returns a sorted, deduplicated list of AZs from Hypervisor CRD labels.
func availabilityZones(hvs []hv1.Hypervisor) []string {
	azSet := make(map[string]struct{})
	for _, hv := range hvs {
		if az, ok := hv.Labels["topology.kubernetes.io/zone"]; ok && az != "" {
			azSet[az] = struct{}{}
		}
	}
	azs := make([]string, 0, len(azSet))
	for az := range azSet {
		azs = append(azs, az)
	}
	sort.Strings(azs)
	return azs
}

// countInstancesInAZ counts total VM instances across all hypervisors in the given AZ.
func countInstancesInAZ(hvs []hv1.Hypervisor, az string) int64 {
	var total int64
	for _, hv := range hvs {
		if hv.Labels["topology.kubernetes.io/zone"] != az {
			continue
		}
		total += int64(len(hv.Status.Instances)) //nolint:gosec
	}
	return total
}

// crdNameFor produces a valid DNS subdomain name for a (flavorGroup, az) pair.
// Underscores and dots are replaced with dashes; the result is lowercased.
func crdNameFor(flavorGroup, az string) string {
	combined := flavorGroup + "-" + az
	combined = strings.ToLower(combined)
	combined = strings.ReplaceAll(combined, "_", "-")
	combined = strings.ReplaceAll(combined, ".", "-")
	return combined
}
