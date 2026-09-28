// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"
	"sort"
	"time"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
)

// RepairReconciler periodically repairs CommittedResource reservation slots:
//  1. Stale removal — VMs no longer active on the slot's target host.
//  2. Duplicate removal — same VM UUID in multiple slots (removed from all; Phase 2 re-assigns).
//  3. Fill partial slots with more VMs from the same host.
//  4. Fill empty slots from the same host, or relocate to a host with fitting VMs.
type RepairReconciler struct {
	client.Client
	Conf     RepairReconcilerConfig
	VMSource reservations.VMSource
	Monitor  RepairReconcilerMonitor
}

func (r *RepairReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	start := time.Now()

	var cr v1alpha1.CommittedResource
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log := LoggerFromContext(ctx).WithValues("component", "cr-repair", "committedResource", req.Name)

	minInterval := r.Conf.MinInterval.Duration
	maxInterval := r.Conf.MaxInterval.Duration

	// Skip non-memory CRs, missing AZ, inactive or out-of-window CRs.
	if cr.Spec.ResourceType != v1alpha1.CommittedResourceTypeMemory {
		return ctrl.Result{}, nil
	}
	if cr.Spec.AvailabilityZone == "" {
		return ctrl.Result{}, nil
	}
	if !cr.IsActive() {
		return ctrl.Result{}, nil
	}
	if cr.Spec.EndTime != nil && cr.Spec.EndTime.Time.Before(start) {
		return ctrl.Result{}, nil
	}
	if cr.Spec.StartTime != nil && cr.Spec.StartTime.After(start) {
		return ctrl.Result{}, nil
	}

	// Min-interval gate: skip if a repair ran too recently.
	if cr.Status.LastRepairAt != nil {
		if elapsed := time.Since(cr.Status.LastRepairAt.Time); elapsed < minInterval {
			return ctrl.Result{RequeueAfter: minInterval - elapsed}, nil
		}
	}

	log = log.WithValues("projectID", cr.Spec.ProjectID)
	log.Info("repair reconcile starting")

	// Load hypervisors, flavor group, and all sibling reservations
	azHVItems, activeOnHV, err := r.loadAZHypervisors(ctx, cr.Spec.AvailabilityZone)
	if err != nil {
		return ctrl.Result{}, err
	}
	flavorNames, err := r.loadFlavorNames(ctx, cr.Spec.FlavorGroupName)
	if err != nil {
		return ctrl.Result{}, err
	}
	if flavorNames == nil {
		log.Info("flavor group not found, deferring repair", "flavorGroupName", cr.Spec.FlavorGroupName)
		return ctrl.Result{RequeueAfter: minInterval}, nil
	}
	allRes, currentIdx, err := r.loadProjectReservations(ctx, cr.Spec.ProjectID, cr.Spec.FlavorGroupName, cr.Spec.CommitmentUUID)
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(currentIdx) == 0 {
		log.Info("no reservations found for current CR, deferring repair")
		return ctrl.Result{RequeueAfter: minInterval}, nil
	}

	// Phase 1: remove stale and duplicate allocations, then apply patches
	// Abort on any patch error so the exclusion set is built from consistent state
	origP1 := snapshotReservations(allRes)
	modified1 := phase1Cleanup(allRes, currentIdx, activeOnHV)
	for idx := range modified1 {
		if err := r.Patch(ctx, &allRes[idx], client.MergeFrom(origP1[idx])); err != nil {
			log.Error(err, "phase 1: patch failed", "reservation", allRes[idx].Name)
			return ctrl.Result{}, err
		}
	}

	// Build exclusion set from post-Phase-1 state, then fetch unassigned VMs.
	exclusion := buildExclusionSet(allRes)
	unassigned, err := r.buildUnassignedVMs(ctx, &cr, azHVItems, flavorNames, exclusion)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Phase 2: fill partial and empty slots, then apply patches (soft-fail per slot).
	origP2 := snapshotReservations(allRes)
	modified2 := phase2Fill(allRes, currentIdx, unassigned, exclusion)
	patchedP2 := int32(0)
	var phase2Err error
	for idx := range modified2 {
		if err := r.Patch(ctx, &allRes[idx], client.MergeFrom(origP2[idx])); err != nil {
			log.Error(err, "phase 2: patch failed", "reservation", allRes[idx].Name)
			if phase2Err == nil {
				phase2Err = err
			}
		} else {
			patchedP2++
		}
	}

	// Update CR status and emit metrics (even on partial Phase 2 failure).
	slotsFixed := int32(len(modified1)) + patchedP2 //nolint:gosec
	now := metav1.Now()
	old := cr.DeepCopy()
	cr.Status.LastRepairAt = &now
	cr.Status.LastRepairSlotsFixed = slotsFixed
	if err := r.Status().Patch(ctx, &cr, client.MergeFrom(old)); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if slotsFixed > 0 {
		r.Monitor.slotsFixedTotal.WithLabelValues(cr.Spec.FlavorGroupName, cr.Spec.AvailabilityZone).Add(float64(slotsFixed))
	}
	var totalCommitted, totalAssigned int64
	for _, idx := range currentIdx {
		totalCommitted += repairSlotMemoryBytes(&allRes[idx])
		totalAssigned += repairAssignedMemoryBytes(&allRes[idx])
	}
	if totalCommitted > 0 {
		r.Monitor.slotFillRatio.Observe(float64(totalAssigned) / float64(totalCommitted))
	}

	log.Info("repair reconcile complete",
		"slotsFixed", slotsFixed,
		"phase1Modified", len(modified1),
		"phase2Patched", patchedP2,
	)

	if phase2Err != nil {
		return ctrl.Result{}, phase2Err
	}
	return ctrl.Result{RequeueAfter: maxInterval}, nil
}

// loadAZHypervisors returns hypervisors in the given AZ and a per-HV set of active VM UUIDs.
func (r *RepairReconciler) loadAZHypervisors(ctx context.Context, az string) ([]hv1.Hypervisor, map[string]map[string]bool, error) {
	var hvList hv1.HypervisorList
	if err := r.List(ctx, &hvList); err != nil {
		return nil, nil, err
	}
	activeOnHV := make(map[string]map[string]bool, len(hvList.Items))
	var azItems []hv1.Hypervisor
	for i := range hvList.Items {
		hv := &hvList.Items[i]
		if hv.Labels["topology.kubernetes.io/zone"] != az {
			continue
		}
		azItems = append(azItems, *hv)
		active := make(map[string]bool, len(hv.Status.Instances))
		for _, inst := range hv.Status.Instances {
			if inst.Active {
				active[inst.ID] = true
			}
		}
		activeOnHV[hv.Name] = active
	}
	return azItems, activeOnHV, nil
}

// loadFlavorNames returns the set of flavor names for fgName, or nil if the group is unknown.
func (r *RepairReconciler) loadFlavorNames(ctx context.Context, fgName string) (map[string]bool, error) {
	knowledge := &reservations.FlavorGroupKnowledgeClient{Client: r.Client}
	fgs, err := knowledge.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		return nil, err
	}
	fg, ok := fgs[fgName]
	if !ok {
		return nil, nil
	}
	names := make(map[string]bool, len(fg.Flavors))
	for _, f := range fg.Flavors {
		names[f.Name] = true
	}
	return names, nil
}

// loadProjectReservations returns all reservations for the project+flavorGroup,
// plus the slice indices that belong to the current CR (matched by commitmentUUID).
func (r *RepairReconciler) loadProjectReservations(ctx context.Context, projectID, fgName, commitmentUUID string) ([]v1alpha1.Reservation, []int, error) {
	var siblingCRs v1alpha1.CommittedResourceList
	if err := r.List(ctx, &siblingCRs, client.MatchingFields{idxCommittedResourceByProjectID: projectID}); err != nil {
		return nil, nil, err
	}
	var allRes []v1alpha1.Reservation
	var currentIdx []int
	for _, sib := range siblingCRs.Items {
		if sib.Spec.FlavorGroupName != fgName || sib.Spec.ResourceType != v1alpha1.CommittedResourceTypeMemory {
			continue
		}
		var resList v1alpha1.ReservationList
		if err := r.List(ctx, &resList, client.MatchingFields{idxReservationByCommitmentUUID: sib.Spec.CommitmentUUID}); err != nil {
			return nil, nil, err
		}
		for _, res := range resList.Items {
			if res.Spec.CommittedResourceReservation == nil {
				continue
			}
			idx := len(allRes)
			allRes = append(allRes, res)
			if sib.Spec.CommitmentUUID == commitmentUUID {
				currentIdx = append(currentIdx, idx)
			}
		}
	}
	return allRes, currentIdx, nil
}

// buildUnassignedVMs returns a per-HV map of project VMs in the flavor group not yet in any slot.
func (r *RepairReconciler) buildUnassignedVMs(ctx context.Context, cr *v1alpha1.CommittedResource, azHVItems []hv1.Hypervisor, flavorNames map[string]bool, exclusion map[string]struct{}) (map[string][]reservations.VM, error) {
	// ListVMsOnHypervisors with trustHypervisorLocation=true is used instead of ListVMsByProject
	// so that VM host assignments come from the HV CRD (authoritative) rather than the Nova DB
	// (which lags during live migrations). TODO: revisit when the VM CRD is available.
	azVMs, err := r.VMSource.ListVMsOnHypervisors(ctx, &hv1.HypervisorList{Items: azHVItems}, true)
	if err != nil {
		return nil, err
	}
	unassigned := make(map[string][]reservations.VM)
	for _, vm := range azVMs {
		if vm.ProjectID != cr.Spec.ProjectID || !flavorNames[vm.FlavorName] {
			continue
		}
		if _, excluded := exclusion[vm.UUID]; excluded {
			continue
		}
		unassigned[vm.CurrentHypervisor] = append(unassigned[vm.CurrentHypervisor], vm)
	}
	for hvName := range unassigned {
		sortVMsDescByMemory(unassigned[hvName])
	}
	return unassigned, nil
}

// snapshotReservations deep-copies each element for use as MergeFrom originals.
func snapshotReservations(res []v1alpha1.Reservation) []*v1alpha1.Reservation {
	out := make([]*v1alpha1.Reservation, len(res))
	for i := range res {
		out[i] = res[i].DeepCopy()
	}
	return out
}

// buildExclusionSet returns the set of VM UUIDs already assigned to any slot.
func buildExclusionSet(allRes []v1alpha1.Reservation) map[string]struct{} {
	ex := make(map[string]struct{})
	for i := range allRes {
		for uuid := range allRes[i].Spec.CommittedResourceReservation.Allocations {
			ex[uuid] = struct{}{}
		}
	}
	return ex
}

// phase1Cleanup removes stale allocations (VMs not active on the target host) and cross-slot
// duplicates from allRes in-place. Placement-mode slots (no TargetHost) are fully cleared.
// Returns the set of modified indices.
func phase1Cleanup(allRes []v1alpha1.Reservation, currentIdx []int, activeOnHV map[string]map[string]bool) map[int]struct{} {
	modified := make(map[int]struct{})

	// Stale removal — current CR's slots only.
	for _, idx := range currentIdx {
		res := &allRes[idx]
		if res.Spec.TargetHost == "" {
			// Placement-mode slot: no host to validate against — clear orphaned allocations.
			if len(res.Spec.CommittedResourceReservation.Allocations) > 0 {
				res.Spec.CommittedResourceReservation.Allocations = map[string]v1alpha1.CommittedResourceAllocation{}
				modified[idx] = struct{}{}
			}
			continue
		}
		// TODO: revisit when VM CRD is available — migration means a VM can move hosts
		// without going inactive first, making the active-instance check insufficient.
		for vmUUID := range res.Spec.CommittedResourceReservation.Allocations {
			if !activeOnHV[res.Spec.TargetHost][vmUUID] {
				delete(res.Spec.CommittedResourceReservation.Allocations, vmUUID)
				modified[idx] = struct{}{}
			}
		}
	}

	// Duplicate removal — all project+flavorGroup slots.
	vmToIdx := make(map[string][]int)
	for i := range allRes {
		for uuid := range allRes[i].Spec.CommittedResourceReservation.Allocations {
			vmToIdx[uuid] = append(vmToIdx[uuid], i)
		}
	}
	for uuid, indices := range vmToIdx {
		if len(indices) <= 1 {
			continue
		}
		for _, idx := range indices {
			delete(allRes[idx].Spec.CommittedResourceReservation.Allocations, uuid)
			modified[idx] = struct{}{}
		}
	}

	return modified
}

// phase2Fill assigns unassigned VMs to current-CR slots in-place.
// Pass 1: top up partial slots (have allocations) from the same host.
// Pass 2: fill empty slots from the same host, or relocate to any host with a fitting VM.
// Returns the set of modified indices.
func phase2Fill(allRes []v1alpha1.Reservation, currentIdx []int, unassigned map[string][]reservations.VM, exclusion map[string]struct{}) map[int]struct{} {
	modified := make(map[int]struct{})

	// Pass 1: partial slots.
	for _, idx := range currentIdx {
		res := &allRes[idx]
		if res.Spec.TargetHost == "" || len(res.Spec.CommittedResourceReservation.Allocations) == 0 {
			continue
		}
		if fillSlotFromHost(res, res.Spec.TargetHost, unassigned, exclusion) {
			modified[idx] = struct{}{}
		}
	}

	// Pass 2: empty slots.
	for _, idx := range currentIdx {
		res := &allRes[idx]
		if res.Spec.TargetHost == "" || len(res.Spec.CommittedResourceReservation.Allocations) > 0 {
			continue
		}
		if len(unassigned[res.Spec.TargetHost]) > 0 {
			if fillSlotFromHost(res, res.Spec.TargetHost, unassigned, exclusion) {
				modified[idx] = struct{}{}
			}
			continue
		}
		// No VMs on current host — relocate to any host where the smallest VM fits.
		// Map iteration is intentionally non-deterministic: any host that fits is acceptable.
		slotMem := repairSlotMemoryBytes(res)
		for hvName, vms := range unassigned {
			if len(vms) == 0 || repairVMMemoryBytes(&vms[len(vms)-1]) > slotMem {
				continue
			}
			res.Spec.TargetHost = hvName
			// Slot capacity check above guarantees at least one VM fits; discard the bool.
			_ = fillSlotFromHost(res, hvName, unassigned, exclusion)
			modified[idx] = struct{}{}
			break
		}
	}

	return modified
}

// fillSlotFromHost greedily assigns unassigned VMs from hvName to res until committed memory
// is satisfied. Assigned VMs are removed from unassigned and added to exclusion.
// Returns true if any VMs were assigned.
func fillSlotFromHost(
	res *v1alpha1.Reservation,
	hvName string,
	unassigned map[string][]reservations.VM,
	exclusion map[string]struct{},
) bool {

	slotMem := repairSlotMemoryBytes(res)
	if slotMem <= 0 {
		return false
	}
	remaining := slotMem - repairAssignedMemoryBytes(res)
	if remaining <= 0 {
		return false
	}
	if res.Spec.CommittedResourceReservation.Allocations == nil {
		res.Spec.CommittedResourceReservation.Allocations = make(map[string]v1alpha1.CommittedResourceAllocation)
	}
	vms := unassigned[hvName]
	var leftover []reservations.VM
	assigned := false
	for _, vm := range vms {
		vmMem := repairVMMemoryBytes(&vm)
		if vmMem > remaining {
			leftover = append(leftover, vm)
			continue
		}
		res.Spec.CommittedResourceReservation.Allocations[vm.UUID] = repairMakeAllocation(vm)
		exclusion[vm.UUID] = struct{}{}
		remaining -= vmMem
		assigned = true
	}
	unassigned[hvName] = leftover
	return assigned
}

func repairSlotMemoryBytes(res *v1alpha1.Reservation) int64 {
	q, ok := res.Spec.Resources[hv1.ResourceMemory]
	if !ok {
		return 0
	}
	return q.Value()
}

func repairAssignedMemoryBytes(res *v1alpha1.Reservation) int64 {
	var total int64
	for _, alloc := range res.Spec.CommittedResourceReservation.Allocations {
		if q, ok := alloc.Resources[hv1.ResourceMemory]; ok {
			total += q.Value()
		}
	}
	return total
}

func repairVMMemoryBytes(vm *reservations.VM) int64 {
	q, ok := vm.Resources["memory"]
	if !ok {
		return 0
	}
	return q.Value()
}

func repairMakeAllocation(vm reservations.VM) v1alpha1.CommittedResourceAllocation {
	alloc := v1alpha1.CommittedResourceAllocation{
		CreationTimestamp: metav1.Now(),
		Resources:         make(map[hv1.ResourceName]resource.Quantity, 2),
	}
	if q, ok := vm.Resources["memory"]; ok {
		alloc.Resources[hv1.ResourceMemory] = q
	}
	if q, ok := vm.Resources["vcpus"]; ok {
		alloc.Resources[hv1.ResourceCPU] = q
	}
	return alloc
}

// sortVMsDescByMemory sorts VMs by memory descending; UUID is the deterministic tie-break.
func sortVMsDescByMemory(vms []reservations.VM) {
	sort.Slice(vms, func(i, j int) bool {
		mi, mj := repairVMMemoryBytes(&vms[i]), repairVMMemoryBytes(&vms[j])
		if mi != mj {
			return mi > mj
		}
		return vms[i].UUID < vms[j].UUID
	})
}

// hypervisorToCommittedResourcesForRepair maps a Hypervisor event to the CommittedResources
// of projects that have reservation slots on that host.
func (r *RepairReconciler) hypervisorToCommittedResourcesForRepair(ctx context.Context, obj client.Object) []reconcile.Request {
	hvName := obj.GetName()
	log := LoggerFromContext(ctx).WithValues("component", "cr-repair")

	var reservationList v1alpha1.ReservationList
	if err := r.List(ctx, &reservationList, client.MatchingLabels{
		v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
	}); err != nil {
		log.Error(err, "failed to list reservations for hypervisor event", "hypervisor", hvName)
		return nil
	}

	projectIDs := make(map[string]struct{})
	for _, res := range reservationList.Items {
		if res.Status.Host == hvName && res.Spec.CommittedResourceReservation != nil {
			projectIDs[res.Spec.CommittedResourceReservation.ProjectID] = struct{}{}
		}
	}
	if len(projectIDs) == 0 {
		return nil
	}

	var requests []reconcile.Request
	for projectID := range projectIDs {
		var crList v1alpha1.CommittedResourceList
		if err := r.List(ctx, &crList, client.MatchingFields{idxCommittedResourceByProjectID: projectID}); err != nil {
			log.Error(err, "failed to list CRs for hypervisor event", "hypervisor", hvName, "projectID", projectID)
			return nil
		}
		for _, cr := range crList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: cr.Name},
			})
		}
	}
	return requests
}

// SetupWithManager registers the repair reconciler with the controller manager.
func (r *RepairReconciler) SetupWithManager(mgr ctrl.Manager, mcl *multicluster.Client) error {
	log := ctrl.Log.WithName("committed-resource-repair").WithValues("module", "committed-resources")

	if !r.Conf.Enabled {
		log.Info("repair reconciler disabled by config, not starting")
		return nil
	}

	log.Info("starting repair reconciler", "minInterval", r.Conf.MinInterval.Duration, "maxInterval", r.Conf.MaxInterval.Duration)

	if err := indexCommittedResourceByProjectID(context.Background(), mcl); err != nil {
		return fmt.Errorf("failed to set up committed resource project index: %w", err)
	}
	if err := indexReservationByCommitmentUUID(context.Background(), mcl); err != nil {
		return fmt.Errorf("failed to set up reservation by commitment UUID index: %w", err)
	}

	bldr := multicluster.BuildController(mcl, mgr)

	var err error
	bldr, err = bldr.WatchesMulticluster(
		&v1alpha1.CommittedResource{},
		&handler.EnqueueRequestForObject{},
	)
	if err != nil {
		return err
	}

	bldr, err = bldr.WatchesMulticluster(
		&hv1.Hypervisor{},
		handler.EnqueueRequestsFromMapFunc(r.hypervisorToCommittedResourcesForRepair),
	)
	if err != nil {
		return err
	}

	// MaxConcurrentReconciles=1: concurrent runs could assign the same VM to multiple slots.
	return bldr.Named("committed-resource-repair").
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Complete(r)
}
