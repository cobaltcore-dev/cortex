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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	schedulerapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/scheduling"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
)

var log = ctrl.Log.WithName("capacity-controller").WithValues("module", "capacity")

// Controller reconciles FlavorGroupCapacity CRDs on a fixed interval.
// For each AZ it probes all flavor groups, runs the round-robin capacity split, then writes
// one FlavorGroupCapacity CRD per (flavor group × AZ) pair.
type Controller struct {
	client          client.Client
	vmSource        reservations.VMSource
	schedulerClient *reservations.SchedulerClient
	config          Config
}

func NewController(c client.Client, config Config, vmSource reservations.VMSource) *Controller {
	return &Controller{
		client:          c,
		vmSource:        vmSource,
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
			cycleCtx := WithNewGlobalRequestID(ctx)
			if err := c.reconcileAll(cycleCtx); err != nil {
				LoggerFromContext(cycleCtx).Error(err, "reconcile cycle failed")
			}
			timer.Reset(c.config.ReconcileInterval.Duration)
		}
	}
}

type vmUsageKey struct{ group, az string }

// vmUsage aggregates resource totals for running VMs in one (group × AZ).
// resources keys are ResourceMemory (bytes) and ResourceCores (count).
type vmUsage struct {
	instances int64
	resources map[string]int64
}

// reconcileAll iterates all AZs, runs the round-robin split per AZ, then writes CRDs.
func (c *Controller) reconcileAll(ctx context.Context) error {
	logger := LoggerFromContext(ctx)
	startTime := time.Now()

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

	blockedByReservations, err := c.blockedMemoryByHost(ctx)
	if err != nil {
		logger.Error(err, "failed to compute blocked memory by host, placeable slot counts may be overstated")
		blockedByReservations = map[string]int64{}
	}

	usageByKey := c.computeVMUsage(ctx, logger, flavorGroups, hvList.Items)

	var succeeded, failed int
	for _, az := range azs {
		if err := c.reconcileAZ(ctx, az, flavorGroups, hvByName, blockedByReservations, usageByKey); err != nil {
			logger.Error(err, "failed to reconcile AZ", "az", az)
			failed++
			continue
		}
		succeeded += len(flavorGroups)
	}

	logger.Info("capacity reconcile cycle completed",
		"flavorGroups", len(flavorGroups),
		"availabilityZones", len(azs),
		"hypervisors", len(hvList.Items),
		"succeeded", succeeded,
		"failed", failed,
		"duration", time.Since(startTime).String())
	return nil
}

// computeVMUsage fetches running VMs and aggregates usage per (flavorGroup, az).
// On error returns an empty map — capacity reporting continues with zero usage.
func (c *Controller) computeVMUsage(
	ctx context.Context,
	logger interface{ Error(error, string, ...any) },
	flavorGroups map[string]compute.FlavorGroupFeature,
	hvs []hv1.Hypervisor,
) map[vmUsageKey]vmUsage {

	result := make(map[vmUsageKey]vmUsage)
	if c.vmSource == nil {
		return result
	}

	hvList := &hv1.HypervisorList{Items: hvs}
	vms, err := c.vmSource.ListVMsOnHypervisors(ctx, hvList, true)
	if err != nil {
		logger.Error(err, "failed to list VMs for usage computation, usage will be reported as zero")
		return result
	}

	flavorToGroup := make(map[string]string)
	flavorMemBytes := make(map[string]int64)
	flavorVCPUs := make(map[string]int64)
	for groupName, gd := range flavorGroups {
		for _, f := range gd.Flavors {
			flavorToGroup[f.Name] = groupName
			flavorMemBytes[f.Name] = int64(f.MemoryMB) * 1024 * 1024 //nolint:gosec
			flavorVCPUs[f.Name] = int64(f.VCPUs)                     //nolint:gosec
		}
	}

	for _, vm := range vms {
		groupName, ok := flavorToGroup[vm.FlavorName]
		if !ok {
			continue
		}
		key := vmUsageKey{group: groupName, az: vm.AvailabilityZone}
		u := result[key]
		if u.resources == nil {
			u.resources = make(map[string]int64)
		}
		u.instances++
		u.resources[ResourceMemory] += flavorMemBytes[vm.FlavorName]
		u.resources[ResourceCores] += flavorVCPUs[vm.FlavorName]
		result[key] = u
	}
	return result
}

// hvRemainingResources returns remaining schedulable resources after subtracting
// current allocations and (for memory) active reservation blocks.
// Returns nil if the hypervisor has no capacity data.
func hvRemainingResources(hv hv1.Hypervisor, blockedMemBytes int64) map[string]int64 {
	effCap := hv.Status.EffectiveCapacity
	if effCap == nil {
		effCap = hv.Status.Capacity
	}
	if effCap == nil {
		return nil
	}

	result := make(map[string]int64, 2)

	if qty, ok := effCap[hv1.ResourceMemory]; ok {
		mem := qty.Value()
		if alloc, ok := hv.Status.Allocation[hv1.ResourceMemory]; ok {
			mem -= alloc.Value()
		}
		mem -= blockedMemBytes
		if mem < 0 {
			mem = 0
		}
		result[ResourceMemory] = mem
	}

	if qty, ok := effCap[hv1.ResourceCPU]; ok {
		cpu := qty.Value()
		if alloc, ok := hv.Status.Allocation[hv1.ResourceCPU]; ok {
			cpu -= alloc.Value()
		}
		if cpu < 0 {
			cpu = 0
		}
		result[ResourceCores] = cpu
	}

	return result
}

// reconcileAZ runs the round-robin capacity split for all flavor groups in one AZ,
// then writes one FlavorGroupCapacity CRD per group that had all probes succeed.
// Groups with failed probes are skipped — their CRDs retain the last good state.
func (c *Controller) reconcileAZ(
	ctx context.Context,
	az string,
	flavorGroups map[string]compute.FlavorGroupFeature,
	hvByName map[string]hv1.Hypervisor,
	blockedByReservations map[string]int64,
	usageByKey map[vmUsageKey]vmUsage,
) error {

	logger := LoggerFromContext(ctx)

	type probeResult struct {
		groupName string
		groupData compute.FlavorGroupFeature
		flavors   []v1alpha1.FlavorCapacityStatus
		// allFresh is false if any scheduler probe failed; the group's CRD is left unchanged.
		allFresh           bool
		smallestCandidates []string
		committedCapacity  int64
	}

	results := make([]probeResult, 0, len(flavorGroups))

	groupNames := make([]string, 0, len(flavorGroups))
	for name := range flavorGroups {
		groupNames = append(groupNames, name)
	}
	sort.Strings(groupNames)

	for _, groupName := range groupNames {
		groupData := flavorGroups[groupName]

		smallestFlavorBytes := int64(groupData.SmallestFlavor.MemoryMB) * 1024 * 1024 //nolint:gosec
		if smallestFlavorBytes <= 0 {
			logger.Error(fmt.Errorf("smallest flavor %q has invalid memory %d MB",
				groupData.SmallestFlavor.Name, groupData.SmallestFlavor.MemoryMB),
				"skipping flavor group", "flavorGroup", groupName)
			continue
		}

		// Probe all flavors. Sort for stable CRD output.
		flavors := make([]compute.FlavorInGroup, len(groupData.Flavors))
		copy(flavors, groupData.Flavors)
		sort.Slice(flavors, func(i, j int) bool { return flavors[i].Name < flavors[j].Name })

		allFresh := true
		newFlavors := make([]v1alpha1.FlavorCapacityStatus, 0, len(flavors))

		// Load existing per-flavor data to preserve stale values on probe failure.
		crdName := crdNameFor(groupName, az)
		var existing v1alpha1.FlavorGroupCapacity
		if err := c.client.Get(ctx, types.NamespacedName{Name: crdName}, &existing); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get FlavorGroupCapacity %s: %w", crdName, err)
		}
		existingByName := make(map[string]v1alpha1.FlavorCapacityStatus, len(existing.Status.Flavors))
		for _, f := range existing.Status.Flavors {
			existingByName[f.FlavorName] = f
		}

		var smallestCandidates []string
		for _, flavor := range flavors {
			cur := existingByName[flavor.Name]
			cur.FlavorName = flavor.Name

			totalVMSlots, totalHosts, _, totalErr := c.probeScheduler(ctx, flavor, az, c.config.TotalPipeline, hvByName, true, nil)
			placeableVMs, placeableHosts, candidates, placeableErr := c.probeScheduler(ctx, flavor, az, c.config.PlaceablePipeline, hvByName, false, blockedByReservations)

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
				if flavor.Name == groupData.SmallestFlavor.Name {
					smallestCandidates = candidates
				}
			}
			newFlavors = append(newFlavors, cur)
		}

		committedCapacity, committedErr := c.sumCommittedCapacity(ctx, groupName, az, smallestFlavorBytes)
		if committedErr != nil {
			logger.Error(committedErr, "failed to sum committed capacity",
				"flavorGroup", groupName, "az", az)
			committedCapacity = 0
		}

		results = append(results, probeResult{
			groupName:          groupName,
			groupData:          groupData,
			flavors:            newFlavors,
			allFresh:           allFresh,
			smallestCandidates: smallestCandidates,
			committedCapacity:  committedCapacity,
		})
	}

	// Build HostState and GroupInput for the round-robin split.
	// Only include groups where all probes succeeded.
	hosts := make(map[string]HostState)
	groupInputs := make([]GroupInput, 0, len(results))
	for _, r := range results {
		if !r.allFresh || r.smallestCandidates == nil {
			continue
		}
		flavorMemBytes := int64(r.groupData.SmallestFlavor.MemoryMB) * 1024 * 1024 //nolint:gosec
		flavorVCPUs := int64(r.groupData.SmallestFlavor.VCPUs)                     //nolint:gosec

		candidateHosts := make([]string, 0, len(r.smallestCandidates))
		for _, h := range r.smallestCandidates {
			candidateHosts = append(candidateHosts, h)
			if _, ok := hosts[h]; !ok {
				hv, hvOk := hvByName[h]
				if !hvOk {
					continue
				}
				remaining := hvRemainingResources(hv, blockedByReservations[h])
				if remaining != nil {
					hosts[h] = HostState{Remaining: remaining}
				}
			}
		}
		sort.Strings(candidateHosts) // stable order
		groupInputs = append(groupInputs, GroupInput{
			Name: r.groupName,
			FlavorResources: map[string]int64{
				ResourceMemory: flavorMemBytes,
				ResourceCores:  flavorVCPUs,
			},
			CandidateHosts: candidateHosts,
		})
	}

	freeResources, exclusiveResources, unassigned := SplitCapacity(groupInputs, hosts)
	if unassigned[ResourceMemory] > 0 || unassigned[ResourceCores] > 0 {
		logger.Info("fragmented capacity not assigned to any group",
			"az", az, "unassignedMemoryBytes", unassigned[ResourceMemory], "unassignedCores", unassigned[ResourceCores])
	}

	// Write one CRD per group. Skip groups with failed probes — their CRDs retain last good state.
	for _, r := range results {
		if !r.allFresh {
			continue
		}
		if err := c.writeCRD(ctx, r.groupName, r.groupData, az,
			r.flavors, r.committedCapacity,
			usageByKey[vmUsageKey{r.groupName, az}],
			freeResources[r.groupName],
			exclusiveResources[r.groupName],
		); err != nil {
			logger.Error(err, "failed to write FlavorGroupCapacity CRD",
				"flavorGroup", r.groupName, "az", az)
		}
	}
	return nil
}

// writeCRD upserts one FlavorGroupCapacity CRD with fresh computed values.
func (c *Controller) writeCRD(
	ctx context.Context,
	groupName string,
	groupData compute.FlavorGroupFeature,
	az string,
	newFlavors []v1alpha1.FlavorCapacityStatus,
	committedCapacity int64,
	usage vmUsage,
	freeRes map[string]int64,
	exclusiveRes map[string]int64,
) error {

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

	// TotalCapacity: for each flavor multiply slot count by its resources; take the max
	// across all flavors independently. The flavor best matching the host's resource
	// ratio saturates more resources and produces a higher product.
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

	patch := client.MergeFrom(existing.DeepCopy())
	existing.Status.Flavors = newFlavors
	existing.Status.CommittedCapacity = committedCapacity
	existing.Status.TotalCapacity = map[string]resource.Quantity{
		string(v1alpha1.CommittedResourceTypeMemory): *resource.NewQuantity(maxMemBytes, resource.BinarySI),
		string(v1alpha1.CommittedResourceTypeCores):  *resource.NewQuantity(maxCPUCores, resource.DecimalSI),
	}
	existing.Status.RunningInstances = usage.instances
	existing.Status.RunningResources = resMapToQuantity(usage.resources)
	existing.Status.FreeCapacity = resMapToQuantity(freeRes)
	existing.Status.ExclusivelyFreeCapacity = resMapToQuantity(exclusiveRes)
	existing.Status.LastReconcileAt = metav1.Now()

	freshCondition := metav1.Condition{
		Type:               v1alpha1.FlavorGroupCapacityConditionReady,
		ObservedGeneration: existing.Generation,
		Status:             metav1.ConditionTrue,
		Reason:             "ReconcileSucceeded",
		Message:            "capacity data is up-to-date",
	}
	meta.SetStatusCondition(&existing.Status.Conditions, freshCondition)

	if patchErr := c.client.Status().Patch(ctx, &existing, patch); patchErr != nil {
		return fmt.Errorf("failed to patch FlavorGroupCapacity %s status: %w", crdName, patchErr)
	}
	return nil
}

// probeScheduler calls the scheduler with the given pipeline and returns:
//   - aggregated slot count across all returned hosts
//   - number of returned hosts
//   - candidate host names: hosts with remaining capacity > 0
//
// When ignoreAllocations is true (total probe), raw effective capacity is used.
// When false (placeable probe), hv.Status.Allocation and blockedByReservations are subtracted.
func (c *Controller) probeScheduler(
	ctx context.Context,
	flavor compute.FlavorInGroup,
	az, pipeline string,
	hvByName map[string]hv1.Hypervisor,
	ignoreAllocations bool,
	blockedByReservations map[string]int64,
) (capacity, hosts int64, candidateHosts []string, err error) {

	flavorBytes := int64(flavor.MemoryMB) * 1024 * 1024 //nolint:gosec
	if flavorBytes <= 0 {
		return 0, 0, nil, fmt.Errorf("flavor %q has invalid memory %d MB", flavor.Name, flavor.MemoryMB)
	}

	// Build EligibleHosts from all known hypervisors so that novaLimitHostsToRequest
	// (which filters the response to hosts present in the request) does not zero out
	// the result. The AZ filter in the pipeline handles narrowing to the correct AZ.
	eligibleHosts := make([]schedulerapi.ExternalSchedulerHost, 0, len(hvByName))
	for name := range hvByName {
		eligibleHosts = append(eligibleHosts, schedulerapi.ExternalSchedulerHost{ComputeHost: name})
	}

	resp, err := c.schedulerClient.ScheduleReservation(ctx, reservations.ScheduleReservationRequest{
		InstanceUUID:     "capacity-" + flavor.Name,
		ProjectID:        "cortex-capacity-probe",
		FlavorName:       flavor.Name,
		MemoryMB:         flavor.MemoryMB,
		VCPUs:            flavor.VCPUs,
		FlavorExtraSpecs: flavor.ExtraSpecs,
		AvailabilityZone: az,
		Pipeline:         pipeline,
		EligibleHosts:    eligibleHosts,
	}, scheduling.Options{
		ReadOnly:                      true,
		SkipHistory:                   true,
		SkipInflight:                  true,
		SkipCommittedResourceTracking: true,
	})
	if err != nil {
		return 0, 0, nil, fmt.Errorf("scheduler call failed (pipeline=%s): %w", pipeline, err)
	}

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
		capBytes := memCap.Value()
		if !ignoreAllocations {
			if alloc, ok := hv.Status.Allocation[hv1.ResourceMemory]; ok {
				capBytes -= alloc.Value()
			}
			capBytes -= blockedByReservations[hostName]
			if capBytes < 0 {
				capBytes = 0
			}
		}
		if capBytes > 0 {
			capacity += capBytes / flavorBytes
			candidateHosts = append(candidateHosts, hostName)
		}
	}
	hosts = int64(len(candidateHosts))
	return capacity, hosts, candidateHosts, nil
}

// blockedMemoryByHost lists all Reservations and returns the total bytes blocked per host name.
// Only placed reservations (TargetHost or Status.Host non-empty) are counted.
// When a reservation is being migrated (TargetHost != Status.Host), both hosts are blocked.
func (c *Controller) blockedMemoryByHost(ctx context.Context) (map[string]int64, error) {
	var list v1alpha1.ReservationList
	if err := c.client.List(ctx, &list); err != nil {
		return nil, fmt.Errorf("failed to list reservations: %w", err)
	}

	blocked := make(map[string]int64)
	for i := range list.Items {
		res := &list.Items[i]

		hostsToBlock := make(map[string]struct{})
		if res.Spec.TargetHost != "" {
			hostsToBlock[res.Spec.TargetHost] = struct{}{}
		}
		if res.Status.Host != "" {
			hostsToBlock[res.Status.Host] = struct{}{}
		}
		if len(hostsToBlock) == 0 {
			continue
		}

		resourcesToBlock := reservations.UnusedReservationCapacity(res, false)
		memQty, ok := resourcesToBlock[hv1.ResourceMemory]
		if !ok {
			continue
		}
		memBytes := memQty.Value()
		for host := range hostsToBlock {
			blocked[host] += memBytes
		}
	}
	return blocked, nil
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
		if !cr.IsActive() {
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

// resMapToQuantity converts a raw resource map to a map[string]resource.Quantity for CRD
// status fields. Keys are passed through unchanged — ResourceMemory and ResourceCores
// intentionally equal string(v1alpha1.CommittedResourceTypeMemory/Cores) respectively,
// so CRD readers can use either constant to look up values.
func resMapToQuantity(res map[string]int64) map[string]resource.Quantity {
	if len(res) == 0 {
		return nil
	}
	out := make(map[string]resource.Quantity, len(res))
	for k, v := range res {
		switch k {
		case ResourceMemory:
			out[k] = *resource.NewQuantity(v, resource.BinarySI)
		case ResourceCores:
			out[k] = *resource.NewQuantity(v, resource.DecimalSI)
		}
	}
	return out
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
