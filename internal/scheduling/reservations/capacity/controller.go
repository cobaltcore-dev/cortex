// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package capacity

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	schedulerapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
)

var log = ctrl.Log.WithName("capacity-controller").WithValues("module", "capacity")

// Controller reconciles FlavorGroupCapacity CRDs on a fixed interval.
// For each (flavor group × AZ) pair it probes all flavors in the group and updates the CRD status.
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
			timer.Reset(c.config.ReconcileInterval.Duration)
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

	smallestFlavorBytes := int64(groupData.SmallestFlavor.MemoryMB) * 1024 * 1024 //nolint:gosec
	if smallestFlavorBytes <= 0 {
		return fmt.Errorf("smallest flavor %q has invalid memory %d MB",
			groupData.SmallestFlavor.Name, groupData.SmallestFlavor.MemoryMB)
	}

	crdName := crdNameFor(groupName, az)

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

	// Build a lookup of existing per-flavor data so we can preserve stale values on probe failure.
	existingByName := make(map[string]v1alpha1.FlavorCapacityStatus, len(existing.Status.Flavors))
	for _, f := range existing.Status.Flavors {
		existingByName[f.FlavorName] = f
	}

	// Probe all flavors in the group. Sort for stable CRD output.
	flavors := make([]compute.FlavorInGroup, len(groupData.Flavors))
	copy(flavors, groupData.Flavors)
	sort.Slice(flavors, func(i, j int) bool { return flavors[i].Name < flavors[j].Name })

	allFresh := true
	newFlavors := make([]v1alpha1.FlavorCapacityStatus, 0, len(flavors))
	for _, flavor := range flavors {
		cur := existingByName[flavor.Name]
		cur.FlavorName = flavor.Name

		totalVMSlots, totalHosts, totalErr := c.probeScheduler(ctx, flavor, az, c.config.TotalPipeline, hvByName)
		placeableVMs, placeableHosts, placeableErr := c.probeScheduler(ctx, flavor, az, c.config.PlaceablePipeline, hvByName)

		if totalErr != nil {
			allFresh = false
		} else {
			cur.TotalCapacityVMSlots = totalVMSlots
			cur.TotalCapacityHosts = totalHosts
		}
		if placeableErr != nil {
			allFresh = false
		} else {
			cur.PlaceableVMs = placeableVMs
			cur.PlaceableHosts = placeableHosts
		}
		newFlavors = append(newFlavors, cur)
	}

	// Count total instances and committed capacity (always available regardless of probe results).
	totalInstances := countInstancesInAZ(allHVs, az)
	committedCapacity, committedErr := c.sumCommittedCapacity(ctx, groupName, az, smallestFlavorBytes)
	if committedErr != nil {
		log.Error(committedErr, "failed to sum committed capacity", "flavorGroup", groupName, "az", az)
		committedCapacity = 0
	}

	// Compute TotalCapacity: for each flavor multiply slot count by its RAM/CPU,
	// then take the max across all flavors independently for each resource.
	// This reveals the most capacity because the flavor best matching the host's
	// resource ratio saturates more resources and produces a higher product.
	flavorSpecByName := make(map[string]compute.FlavorInGroup, len(groupData.Flavors))
	for _, f := range groupData.Flavors {
		flavorSpecByName[f.Name] = f
	}
	var maxMemBytes, maxCPUCores int64
	for _, f := range newFlavors {
		spec, ok := flavorSpecByName[f.FlavorName]
		if !ok || f.TotalCapacityVMSlots <= 0 {
			continue
		}
		memBytes := f.TotalCapacityVMSlots * int64(spec.MemoryMB) * 1024 * 1024 //nolint:gosec
		cpuCores := f.TotalCapacityVMSlots * int64(spec.VCPUs)                  //nolint:gosec
		if memBytes > maxMemBytes {
			maxMemBytes = memBytes
		}
		if cpuCores > maxCPUCores {
			maxCPUCores = cpuCores
		}
	}

	// Only update TotalCapacity when all probes succeeded (allFresh=true).
	// This preserves stale values across transient probe failures and ensures
	// the CR controller can distinguish "not yet probed" (key absent) from
	// "probed but zero capacity" (key present, value=0).
	var totalCapacity map[string]resource.Quantity
	if allFresh {
		totalCapacity = map[string]resource.Quantity{
			string(v1alpha1.CommittedResourceTypeMemory): *resource.NewQuantity(maxMemBytes, resource.BinarySI),
			string(v1alpha1.CommittedResourceTypeCores):  *resource.NewQuantity(maxCPUCores, resource.DecimalSI),
		}
	} else {
		totalCapacity = existing.Status.TotalCapacity
	}

	patch := client.MergeFrom(existing.DeepCopy())
	existing.Status.Flavors = newFlavors
	existing.Status.TotalInstances = totalInstances
	existing.Status.CommittedCapacity = committedCapacity
	existing.Status.TotalCapacity = totalCapacity
	existing.Status.LastReconcileAt = metav1.Now()

	freshCondition := metav1.Condition{
		Type:               v1alpha1.FlavorGroupCapacityConditionReady,
		ObservedGeneration: existing.Generation,
	}
	if allFresh {
		freshCondition.Status = metav1.ConditionTrue
		freshCondition.Reason = "ReconcileSucceeded"
		freshCondition.Message = "capacity data is up-to-date"
	} else {
		freshCondition.Status = metav1.ConditionFalse
		freshCondition.Reason = "ReconcileFailed"
		freshCondition.Message = "one or more flavor probes failed"
	}
	meta.SetStatusCondition(&existing.Status.Conditions, freshCondition)

	if patchErr := c.client.Status().Patch(ctx, &existing, patch); patchErr != nil {
		return fmt.Errorf("failed to patch FlavorGroupCapacity %s status: %w", crdName, patchErr)
	}
	return nil
}

// probeScheduler calls the scheduler with the given pipeline and returns VM slots + host count.
// Capacity is computed as sum of floor(hostMemory / flavorMemory) across returned hosts.
func (c *Controller) probeScheduler(
	ctx context.Context,
	flavor compute.FlavorInGroup,
	az, pipeline string,
	hvByName map[string]hv1.Hypervisor,
) (capacity, hosts int64, err error) {

	flavorBytes := int64(flavor.MemoryMB) * 1024 * 1024 //nolint:gosec
	if flavorBytes <= 0 {
		return 0, 0, fmt.Errorf("flavor %q has invalid memory %d MB", flavor.Name, flavor.MemoryMB)
	}

	// Build EligibleHosts from all known hypervisors so that novaLimitHostsToRequest
	// (which filters the response to hosts present in the request) does not zero out
	// the result. The AZ filter in the pipeline handles narrowing to the correct AZ.
	eligibleHosts := make([]schedulerapi.ExternalSchedulerHost, 0, len(hvByName))
	for name := range hvByName {
		eligibleHosts = append(eligibleHosts, schedulerapi.ExternalSchedulerHost{ComputeHost: name})
	}

	resp, err := c.schedulerClient.ScheduleReservation(ctx, reservations.ScheduleReservationRequest{
		InstanceUUID:     uuid.New().String(),
		ProjectID:        "cortex-capacity-probe",
		FlavorName:       flavor.Name,
		MemoryMB:         flavor.MemoryMB,
		VCPUs:            flavor.VCPUs,
		FlavorExtraSpecs: flavor.ExtraSpecs,
		AvailabilityZone: az,
		Pipeline:         pipeline,
		EligibleHosts:    eligibleHosts,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("scheduler call failed (pipeline=%s): %w", pipeline, err)
	}

	hosts = int64(len(resp.Hosts))
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
			capacity += capBytes / flavorBytes
		}
	}
	return capacity, hosts, nil
}

// sumCommittedCapacity sums AcceptedSpec.Amount (or Spec.Amount as fallback) across all
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
		if cr.Status.AcceptedSpec != nil {
			amount = cr.Status.AcceptedSpec.Amount
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
		total += int64(len(hv.Status.Instances))
	}
	return total
}

// crdNameFor produces a collision-safe DNS label for a (flavorGroup, az) pair.
// A 6-hex-char FNV-1a hash of the raw inputs is appended so that pairs differing only
// by characters that sanitise identically (e.g. "." vs "-") still get unique names.
func crdNameFor(flavorGroup, az string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(flavorGroup + "\x00" + az))
	suffix := fmt.Sprintf("%06x", h.Sum32()&0xFFFFFF)

	prefix := strings.ToLower(flavorGroup + "-" + az)
	prefix = strings.ReplaceAll(prefix, "_", "-")
	prefix = strings.ReplaceAll(prefix, ".", "-")
	if len(prefix) > 56 { // 56 + "-" + 6 = 63 chars (DNS label limit)
		prefix = prefix[:56]
	}
	return prefix + "-" + suffix
}
