// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package quota

import (
	"context"
	"fmt"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	commitments "github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/commitments"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/failover"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = ctrl.Log.WithName("quota-controller").WithValues("module", "quota")

// QuotaController manages quota usage tracking for projects.
// It provides three reconciliation modes:
// - Periodic full reconcile: recomputes all TotalUsage from Postgres
// - Incremental HV diff: delta-updates TotalUsage on HV instance changes
// - PaygUsage-only recompute: triggered by CR or ProjectQuota spec changes
type QuotaController struct {
	client.Client
	VMSource failover.VMSource
	Config   QuotaControllerConfig
	Metrics  *QuotaMetrics
}

// NewQuotaController creates a new QuotaController.
func NewQuotaController(
	c client.Client,
	vmSource failover.VMSource,
	config QuotaControllerConfig,
	metrics *QuotaMetrics,
) *QuotaController {

	return &QuotaController{
		Client:   c,
		VMSource: vmSource,
		Config:   config,
		Metrics:  metrics,
	}
}

// ============================================================================
// Periodic Full Reconciliation
// ============================================================================

// ReconcilePeriodic performs a full reconcile of all project quota usage.
// It reads all VMs from Postgres, computes TotalUsage per project/AZ/resource,
// then derives PaygUsage = TotalUsage - CRUsage for each existing ProjectQuota CRD.
func (c *QuotaController) ReconcilePeriodic(ctx context.Context) error {
	ctx = WithNewGlobalRequestID(ctx)
	startTime := time.Now()
	logger := LoggerFromContext(ctx).WithValues("mode", "full-reconcile")
	logger.Info("starting full quota reconcile")

	// Fetch flavor groups from Knowledge CRD
	flavorGroupClient := &reservations.FlavorGroupKnowledgeClient{Client: c.Client}
	flavorGroups, err := flavorGroupClient.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		logger.Error(err, "failed to get flavor groups")
		c.Metrics.RecordReconcileResult(false)
		return fmt.Errorf("failed to get flavor groups: %w", err)
	}

	// Build flavorName → flavorGroup lookup
	flavorToGroup := buildFlavorToGroupMap(flavorGroups)

	// Fetch all VMs using VMSource (reads from Postgres via DBVMSource)
	vms, err := c.VMSource.ListVMs(ctx)
	if err != nil {
		logger.Error(err, "failed to list VMs")
		c.Metrics.RecordReconcileResult(false)
		return fmt.Errorf("failed to list VMs: %w", err)
	}

	// Compute totalUsage per project/AZ/resource
	totalUsageByProject := c.computeTotalUsage(vms, flavorToGroup, flavorGroups)

	// List all existing ProjectQuota CRDs
	var pqList v1alpha1.ProjectQuotaList
	if err := c.List(ctx, &pqList); err != nil {
		logger.Error(err, "failed to list ProjectQuota CRDs")
		c.Metrics.RecordReconcileResult(false)
		return fmt.Errorf("failed to list ProjectQuota CRDs: %w", err)
	}

	// List all CommittedResource CRDs and pre-group by project ID
	var crList v1alpha1.CommittedResourceList
	if err := c.List(ctx, &crList); err != nil {
		logger.Error(err, "failed to list CommittedResource CRDs")
		c.Metrics.RecordReconcileResult(false)
		return fmt.Errorf("failed to list CommittedResource CRDs: %w", err)
	}
	crsByProject := groupCRsByProject(crList.Items)

	// For each ProjectQuota CRD, write TotalUsage + PaygUsage
	var updated, skipped int
	for i := range pqList.Items {
		pq := &pqList.Items[i]
		projectID := pq.Spec.ProjectID

		// Get totalUsage for this project (may be empty if project has no VMs)
		projectTotalUsage := totalUsageByProject[projectID]

		// Compute CRUsage for this project (using pre-grouped CRs)
		crUsage := c.computeCRUsage(crsByProject[projectID], flavorGroups)

		// Derive PaygUsage
		paygUsage := derivePaygUsage(projectTotalUsage, crUsage)

		// Write status with conflict retry (full reconcile sets LastFullReconcileAt)
		if err := c.updateProjectQuotaStatusWithRetry(ctx, pq.Name, projectTotalUsage, paygUsage, true); err != nil {
			logger.Error(err, "failed to update ProjectQuota status", "project", projectID)
			skipped++
			continue
		}

		// Record metrics
		c.recordUsageMetrics(projectID, projectTotalUsage, paygUsage, crUsage)
		updated++
	}

	duration := time.Since(startTime)
	c.Metrics.RecordReconcileDuration(duration.Seconds())
	c.Metrics.RecordReconcileResult(true)
	logger.Info("full quota reconcile completed",
		"duration", duration.Round(time.Millisecond),
		"totalVMs", len(vms),
		"projectQuotas", len(pqList.Items),
		"updated", updated,
		"skipped", skipped)

	return nil
}

// ============================================================================
// Watch-based Reconciliation (PaygUsage-only recompute)
// ============================================================================

// Reconcile handles watch-based reconciliation for a single ProjectQuota.
// Triggered by: CR Status.UsedAmount changes or ProjectQuota spec changes.
//
// Behavior depends on what changed:
// - Spec change (Generation > ObservedGeneration): recomputes TotalUsage from Postgres + PaygUsage
// - CR UsedAmount change (Generation == ObservedGeneration): reads persisted TotalUsage, recomputes PaygUsage only
func (c *QuotaController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx = WithNewGlobalRequestID(ctx)
	logger := LoggerFromContext(ctx).WithValues("projectQuota", req.Name, "mode", "payg-recompute")
	logger.V(1).Info("reconciling ProjectQuota")

	// Fetch the ProjectQuota
	var pq v1alpha1.ProjectQuota
	if err := c.Get(ctx, req.NamespacedName, &pq); err != nil {
		if client.IgnoreNotFound(err) == nil {
			logger.V(1).Info("ProjectQuota not found, likely deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	projectID := pq.Spec.ProjectID
	ctx = reservations.WithRequestID(ctx, projectID)

	// Determine if this is a spec change (new CRD or quota update) vs. a CR UsedAmount change
	specChanged := pq.Generation > pq.Status.ObservedGeneration

	var totalUsage map[string]map[string]int64
	if specChanged {
		// Spec changed (new CRD or quota update) — recompute TotalUsage from Postgres
		logger.Info("spec changed, recomputing TotalUsage from Postgres",
			"generation", pq.Generation, "observedGeneration", pq.Status.ObservedGeneration)
		var err error
		totalUsage, err = c.computeTotalUsageForProject(ctx, projectID)
		if err != nil {
			logger.Error(err, "failed to compute TotalUsage for project")
			return ctrl.Result{}, err
		}
	} else {
		// CR UsedAmount changed — read persisted TotalUsage, only recompute PaygUsage.
		// Status stores flat map[string]int64 (for this AZ only), but internal functions
		// operate on map[string]map[string]int64. Reconstruct the multi-AZ view.
		if pq.Status.TotalUsage != nil {
			totalUsage = expandAZSlice(pq.Status.TotalUsage, pq.Spec.AvailabilityZone)
		} else {
			// Safety fallback: TotalUsage should always be set after first spec reconcile
			logger.Info("no TotalUsage persisted, computing as fallback")
			var err error
			totalUsage, err = c.computeTotalUsageForProject(ctx, projectID)
			if err != nil {
				logger.Error(err, "failed to compute TotalUsage for project")
				return ctrl.Result{}, err
			}
		}
	}

	// Fetch flavor groups for CRUsage computation
	flavorGroupClient := &reservations.FlavorGroupKnowledgeClient{Client: c.Client}
	flavorGroups, err := flavorGroupClient.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		logger.Error(err, "failed to get flavor groups")
		return ctrl.Result{}, err
	}

	// List CRs for this project (from local cache)
	var crList v1alpha1.CommittedResourceList
	if err := c.List(ctx, &crList); err != nil {
		logger.Error(err, "failed to list CommittedResource CRDs")
		return ctrl.Result{}, err
	}
	crsByProject := groupCRsByProject(crList.Items)

	// Compute CRUsage
	crUsage := c.computeCRUsage(crsByProject[projectID], flavorGroups)

	// Derive PaygUsage
	paygUsage := derivePaygUsage(totalUsage, crUsage)

	// Write updated status with conflict retry
	if err := c.updateProjectQuotaStatusWithRetry(ctx, pq.Name, totalUsage, paygUsage, specChanged); err != nil {
		logger.Error(err, "failed to update ProjectQuota status")
		return ctrl.Result{}, err
	}

	// Record metrics
	c.recordUsageMetrics(projectID, totalUsage, paygUsage, crUsage)

	logger.V(1).Info("reconcile completed", "project", projectID, "specChanged", specChanged)
	return ctrl.Result{}, nil
}

// computeTotalUsageForProject computes TotalUsage for a single project by reading
// all VMs from Postgres and filtering to the target project. Used as bootstrap when
// a ProjectQuota is first created and has no persisted TotalUsage yet.
func (c *QuotaController) computeTotalUsageForProject(ctx context.Context, projectID string) (map[string]map[string]int64, error) {
	// Fetch flavor groups from Knowledge CRD
	flavorGroupClient := &reservations.FlavorGroupKnowledgeClient{Client: c.Client}
	flavorGroups, err := flavorGroupClient.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get flavor groups: %w", err)
	}

	// Build flavorName → flavorGroup lookup
	flavorToGroup := buildFlavorToGroupMap(flavorGroups)

	// Fetch all VMs and compute usage (only the target project's data will be used)
	vms, err := c.VMSource.ListVMs(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list VMs: %w", err)
	}

	// Compute totalUsage for all projects and return just this one
	totalUsageByProject := c.computeTotalUsage(vms, flavorToGroup, flavorGroups)
	return totalUsageByProject[projectID], nil
}

// ============================================================================
// Incremental Update (HV Instance Diff)
// ============================================================================

// usageDelta tracks resource deltas for a single project during incremental reconciliation.
type usageDelta struct {
	// increments[resourceName][az] = amount to add
	increments map[string]map[string]int64
	// decrements[resourceName][az] = amount to subtract
	decrements map[string]map[string]int64
}

func newUsageDelta() *usageDelta {
	return &usageDelta{
		increments: make(map[string]map[string]int64),
		decrements: make(map[string]map[string]int64),
	}
}

func (d *usageDelta) addIncrement(resourceName, az string, amount int64) {
	if d.increments[resourceName] == nil {
		d.increments[resourceName] = make(map[string]int64)
	}
	d.increments[resourceName][az] += amount
}

func (d *usageDelta) addDecrement(resourceName, az string, amount int64) {
	if d.decrements[resourceName] == nil {
		d.decrements[resourceName] = make(map[string]int64)
	}
	d.decrements[resourceName][az] += amount
}

// ReconcileHVDiff handles incremental updates when HV instance lists change.
// It diffs old vs new instances to delta-update TotalUsage for affected projects.
// Deltas are batched per project and applied in a single status update per project
// to avoid race conditions from multiple updates.
func (c *QuotaController) ReconcileHVDiff(ctx context.Context, oldHV, newHV *hv1.Hypervisor) error {
	ctx = WithNewGlobalRequestID(ctx)
	logger := LoggerFromContext(ctx).WithValues("hypervisor", newHV.Name, "mode", "incremental")

	// Diff old vs new instances
	oldInstances := make(map[string]bool)
	for _, inst := range oldHV.Status.Instances {
		if inst.Active {
			oldInstances[inst.ID] = true
		}
	}
	newInstances := make(map[string]bool)
	for _, inst := range newHV.Status.Instances {
		if inst.Active {
			newInstances[inst.ID] = true
		}
	}

	// Find added and removed UUIDs
	var added, removed []string
	for id := range newInstances {
		if !oldInstances[id] {
			added = append(added, id)
		}
	}
	for id := range oldInstances {
		if !newInstances[id] {
			removed = append(removed, id)
		}
	}

	if len(added) == 0 && len(removed) == 0 {
		return nil
	}

	logger.V(1).Info("HV instance diff detected", "added", len(added), "removed", len(removed))

	// Get flavor groups for mapping
	flavorGroupClient := &reservations.FlavorGroupKnowledgeClient{Client: c.Client}
	flavorGroups, err := flavorGroupClient.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		logger.Error(err, "failed to get flavor groups for incremental update")
		return err
	}
	flavorToGroup := buildFlavorToGroupMap(flavorGroups)

	// Accumulate deltas per project (batched to avoid per-VM persist race)
	projectDeltas := make(map[string]*usageDelta)

	// Process added instances
	for _, vmUUID := range added {
		c.accumulateAddedVM(ctx, vmUUID, flavorToGroup, flavorGroups, projectDeltas)
	}

	// Process removed instances
	for _, vmUUID := range removed {
		c.accumulateRemovedVM(ctx, vmUUID, flavorToGroup, flavorGroups, projectDeltas)
	}

	// Apply batched deltas and recompute PaygUsage for affected projects
	var crList v1alpha1.CommittedResourceList
	if err := c.List(ctx, &crList); err != nil {
		logger.Error(err, "failed to list CRs for PaygUsage recompute")
		return err
	}
	crsByProject := groupCRsByProject(crList.Items)

	for projectID, delta := range projectDeltas {
		if err := c.applyDeltaAndUpdateStatus(ctx, projectID, delta, crsByProject[projectID], flavorGroups); err != nil {
			logger.Error(err, "failed to apply delta for project", "project", projectID)
			// Continue with other projects
		}
	}

	return nil
}

// accumulateAddedVM looks up a VM and accumulates its resource contribution as a delta.
// It checks whether the VM is truly new (created after last full reconcile) vs a migration
// (already counted in TotalUsage). Only new VMs get incremented.
func (c *QuotaController) accumulateAddedVM(
	ctx context.Context,
	vmUUID string,
	flavorToGroup map[string]string,
	flavorGroups map[string]compute.FlavorGroupFeature,
	projectDeltas map[string]*usageDelta,
) {

	logger := LoggerFromContext(ctx).WithValues("vmUUID", vmUUID)

	vm, err := c.VMSource.GetVM(ctx, vmUUID)
	if err != nil {
		logger.Error(err, "failed to get VM for increment")
		return
	}
	if vm == nil {
		return // VM not found in DB, skip
	}

	// Check if this VM was already counted in the last full reconcile.
	// If the VM was created BEFORE the last full reconcile, it's a migration
	// (already in TotalUsage) and we should NOT increment again.
	if !c.isVMNewSinceLastReconcile(ctx, vm) {
		logger.V(1).Info("VM already counted (created before last reconcile), skipping increment",
			"vmCreatedAt", vm.CreatedAt, "project", vm.ProjectID)
		return
	}

	groupName, ok := flavorToGroup[vm.FlavorName]
	if !ok {
		return // Flavor not in any group
	}
	if _, ok := flavorGroups[groupName]; !ok {
		return
	}

	ramUnits, coresAmount := vmResourceUnits(vm.Resources)

	delta := projectDeltas[vm.ProjectID]
	if delta == nil {
		delta = newUsageDelta()
		projectDeltas[vm.ProjectID] = delta
	}

	delta.addIncrement(commitments.ResourceNameRAM(groupName), vm.AvailabilityZone, ramUnits)
	delta.addIncrement(commitments.ResourceNameCores(groupName), vm.AvailabilityZone, coresAmount)
}

// isVMNewSinceLastReconcile checks if a VM was created after the last full reconcile.
// Returns true if the VM is new and should be incrementally added to TotalUsage.
// Returns false if the VM already existed at the last full reconcile (migration, not new).
//
// NOTE: There is a known timing gap -- the postgres servers table is only refreshed every
// N minutes by the datasource poller. A VM that was created shortly BEFORE the last reconcile
// might not have been visible in postgres yet (sync delay), so the full reconcile may have
// missed it. In that case we would also skip the increment here (CreatedAt <= LastReconcileAt)
// and the VM would only be counted on the NEXT full reconcile cycle. This is acceptable for
// now and will be resolved when we move to a CRD-based VM source with real-time events.
func (c *QuotaController) isVMNewSinceLastReconcile(ctx context.Context, vm *failover.VM) bool {
	if vm.CreatedAt == "" {
		// No creation time available -- be conservative, skip increment.
		// The next full reconcile will pick it up.
		return false
	}

	// Look up the ProjectQuota for this VM's project
	crdName := "quota-" + vm.ProjectID + "-" + vm.AvailabilityZone
	var pq v1alpha1.ProjectQuota
	if err := c.Get(ctx, client.ObjectKey{Name: crdName}, &pq); err != nil {
		// If we can't find the ProjectQuota, skip (full reconcile will handle it)
		return false
	}

	if pq.Status.LastFullReconcileAt == nil {
		// No full reconcile has run yet -- skip incremental updates
		return false
	}

	// Parse the VM's creation time and compare with last FULL reconcile
	vmCreatedAt, err := time.Parse("2006-01-02T15:04:05Z", vm.CreatedAt)
	if err != nil {
		// Try alternative format with timezone offset
		vmCreatedAt, err = time.Parse(time.RFC3339, vm.CreatedAt)
		if err != nil {
			// Cannot parse -- be conservative, skip
			return false
		}
	}

	return vmCreatedAt.After(pq.Status.LastFullReconcileAt.Time)
}

// accumulateRemovedVM looks up a deleted VM and accumulates its resource contribution as a decrement.
func (c *QuotaController) accumulateRemovedVM(
	ctx context.Context,
	vmUUID string,
	flavorToGroup map[string]string,
	flavorGroups map[string]compute.FlavorGroupFeature,
	projectDeltas map[string]*usageDelta,
) {

	logger := LoggerFromContext(ctx).WithValues("vmUUID", vmUUID)

	// Check if the VM still exists in the servers table (migrated away = still running)
	active, err := c.VMSource.IsServerActive(ctx, vmUUID)
	if err != nil {
		logger.Error(err, "failed to check server for decrement")
		return
	}
	if active {
		// VM still exists (either ACTIVE on another HV, or in non-ACTIVE state).
		// Don't decrement — the full reconcile handles these correctly.
		return
	}

	// Not found in servers table — check deleted_servers
	info, err := c.VMSource.GetDeletedVMInfo(ctx, vmUUID)
	if err != nil {
		logger.Error(err, "failed to get deleted VM info for decrement")
		return
	}
	if info == nil {
		// Not found anywhere — cannot determine what to decrement
		logger.V(1).Info("removed VM not found in servers or deleted_servers")
		return
	}

	groupName, ok := flavorToGroup[info.FlavorName]
	if !ok {
		return // Flavor not in any group
	}
	if _, ok := flavorGroups[groupName]; !ok {
		return
	}

	// Compute commitment units from the resolved flavor resources
	ramUnits := int64(info.RAMMiB) / 1024 //nolint:gosec // safe
	coresAmount := int64(info.VCPUs)      //nolint:gosec // safe

	delta := projectDeltas[info.ProjectID]
	if delta == nil {
		delta = newUsageDelta()
		projectDeltas[info.ProjectID] = delta
	}

	delta.addDecrement(commitments.ResourceNameRAM(groupName), info.AvailabilityZone, ramUnits)
	delta.addDecrement(commitments.ResourceNameCores(groupName), info.AvailabilityZone, coresAmount)
}

// applyDeltaAndUpdateStatus applies batched deltas to ALL per-AZ ProjectQuota CRDs for a project.
// It lists all per-AZ CRDs, applies relevant deltas to each, recomputes PaygUsage, and persists.
func (c *QuotaController) applyDeltaAndUpdateStatus(
	ctx context.Context,
	projectID string,
	delta *usageDelta,
	projectCRs []v1alpha1.CommittedResource,
	flavorGroups map[string]compute.FlavorGroupFeature,
) error {

	// Collect all AZs affected by this delta
	affectedAZs := make(map[string]bool)
	for _, azAmounts := range delta.increments {
		for az := range azAmounts {
			affectedAZs[az] = true
		}
	}
	for _, azAmounts := range delta.decrements {
		for az := range azAmounts {
			affectedAZs[az] = true
		}
	}

	crUsage := c.computeCRUsage(projectCRs, flavorGroups)

	for az := range affectedAZs {
		crdName := "quota-" + projectID + "-" + az

		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var pq v1alpha1.ProjectQuota
			if err := c.Get(ctx, client.ObjectKey{Name: crdName}, &pq); err != nil {
				if client.IgnoreNotFound(err) == nil {
					return nil // PQ for this AZ doesn't exist, skip
				}
				return err
			}

			if pq.Status.TotalUsage == nil {
				pq.Status.TotalUsage = make(map[string]int64)
			}

			// Apply increments for this AZ
			for resourceName, azAmounts := range delta.increments {
				if amount, ok := azAmounts[az]; ok {
					pq.Status.TotalUsage[resourceName] += amount
				}
			}

			// Apply decrements for this AZ
			for resourceName, azAmounts := range delta.decrements {
				if amount, ok := azAmounts[az]; ok {
					pq.Status.TotalUsage[resourceName] -= amount
					if pq.Status.TotalUsage[resourceName] < 0 {
						pq.Status.TotalUsage[resourceName] = 0
					}
				}
			}

			// Derive PaygUsage for this AZ: totalUsage[resource] - crUsage[resource][az]
			pq.Status.PaygUsage = make(map[string]int64)
			for resourceName, totalAmount := range pq.Status.TotalUsage {
				crAmount := int64(0)
				if cr, ok := crUsage[resourceName]; ok {
					if azAmount, ok := cr[az]; ok {
						crAmount = azAmount
					}
				}
				paygAmount := totalAmount - crAmount
				if paygAmount < 0 {
					paygAmount = 0
				}
				pq.Status.PaygUsage[resourceName] = paygAmount
			}

			now := metav1.Now()
			pq.Status.LastReconcileAt = &now
			return c.Status().Update(ctx, &pq)
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// ============================================================================
// Manager Setup
// ============================================================================

// SetupWithManager sets up the watch-based reconciler for PaygUsage recomputes.
func (c *QuotaController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("quota-controller").
		// Watch ProjectQuota for spec/generation changes only (ignore status-only updates
		// to avoid infinite reconcile loops since Reconcile() updates status).
		For(&v1alpha1.ProjectQuota{}, builder.WithPredicates(projectQuotaGenerationChangePredicate())).
		// Watch CommittedResource for status changes (UsedAmount updates)
		Watches(
			&v1alpha1.CommittedResource{},
			handler.EnqueueRequestsFromMapFunc(c.mapCRToProjectQuota),
			builder.WithPredicates(crUsedAmountChangePredicate()),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Complete(c)
}

// SetupHVWatcher sets up a separate controller to watch HV CRD changes
// for incremental TotalUsage updates.
func (c *QuotaController) SetupHVWatcher(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("quota-hv-watcher").
		WatchesRawSource(source.Kind(
			mgr.GetCache(),
			&hv1.Hypervisor{},
			&hvInstanceDiffHandler{controller: c},
			hvInstanceChangePredicate(),
		)).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Complete(reconcile.Func(func(_ context.Context, _ ctrl.Request) (ctrl.Result, error) {
			// The actual work is done in the event handler
			return ctrl.Result{}, nil
		}))
}

// Start implements manager.Runnable for the periodic reconciliation loop.
// It does not block manager startup — the first reconcile fires after a short
// initial delay to allow cache sync.
func (c *QuotaController) Start(ctx context.Context) error {
	log.Info("starting quota controller (periodic)",
		"fullReconcileInterval", c.Config.FullReconcileInterval.Duration,
		"crStateFilter", c.Config.CRStateFilter)

	// Use a short initial delay to allow cache sync before first reconcile
	initialDelay := 5 * time.Second
	timer := time.NewTimer(initialDelay)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("stopping quota controller")
			return nil
		case <-timer.C:
			if err := c.ReconcilePeriodic(ctx); err != nil {
				log.Error(err, "periodic full reconcile failed")
			}
			timer.Reset(c.Config.FullReconcileInterval.Duration)
		}
	}
}

// ============================================================================
// Internal Helpers
// ============================================================================

// computeTotalUsage aggregates VM resources by project/AZ/resource.
//
// The RAM calculation converts server RAM into LIQUID commitment units:
//   - Each flavor group has a "smallest flavor" defining the unit size (e.g., 32768 MiB)
//   - A VM's RAM usage in units = VM_RAM_MiB / unit_size_MiB
//   - Example: a 64 GiB VM in a group with 32 GiB smallest flavor = 2 units
//
// This matches the unit system used by LIQUID for commitment tracking.
// The per-AZ breakdown allows Limes to enforce AZ-level quota limits.
func (c *QuotaController) computeTotalUsage(
	vms []failover.VM,
	flavorToGroup map[string]string,
	flavorGroups map[string]compute.FlavorGroupFeature,
) map[string]map[string]map[string]int64 {
	// result[projectID][resourceName] = ResourceQuotaUsage{PerAZ: {az: amount}}
	result := make(map[string]map[string]map[string]int64)

	for _, vm := range vms {
		groupName, ok := flavorToGroup[vm.FlavorName]
		if !ok {
			continue // Flavor not in any tracked group
		}
		if _, ok := flavorGroups[groupName]; !ok {
			continue
		}

		ramResourceName := commitments.ResourceNameRAM(groupName)
		coresResourceName := commitments.ResourceNameCores(groupName)

		ramUnits, coresAmount := vmResourceUnits(vm.Resources)

		if _, ok := result[vm.ProjectID]; !ok {
			result[vm.ProjectID] = make(map[string]map[string]int64)
		}

		// Accumulate RAM usage for this project + AZ
		ramUsage := result[vm.ProjectID][ramResourceName]
		if ramUsage == nil {
			ramUsage = make(map[string]int64)
		}
		ramUsage[vm.AvailabilityZone] += ramUnits
		result[vm.ProjectID][ramResourceName] = ramUsage

		// Accumulate cores usage for this project + AZ
		coresUsage := result[vm.ProjectID][coresResourceName]
		if coresUsage == nil {
			coresUsage = make(map[string]int64)
		}
		coresUsage[vm.AvailabilityZone] += coresAmount
		result[vm.ProjectID][coresResourceName] = coresUsage
	}

	return result
}

// groupCRsByProject groups CommittedResources by project ID for efficient lookup.
func groupCRsByProject(crs []v1alpha1.CommittedResource) map[string][]v1alpha1.CommittedResource {
	result := make(map[string][]v1alpha1.CommittedResource)
	for i := range crs {
		projectID := crs[i].Spec.ProjectID
		result[projectID] = append(result[projectID], crs[i])
	}
	return result
}

// computeCRUsage computes the committed resource usage from a pre-filtered slice of CRs for one project.
// It reads UsedResources from each CR's status and converts to commitment units (multiples for RAM, raw for cores).
func (c *QuotaController) computeCRUsage(crs []v1alpha1.CommittedResource, flavorGroups map[string]compute.FlavorGroupFeature) map[string]map[string]int64 {
	result := make(map[string]map[string]int64)

	for i := range crs {
		cr := &crs[i]

		// Prefer AcceptedSpec (last successful reconcile snapshot) over Spec
		// to avoid mis-bucketing during spec transitions.
		spec := &cr.Spec
		if cr.Status.AcceptedSpec != nil {
			spec = cr.Status.AcceptedSpec
		}

		// Filter: only matching states
		if !c.isCRStateIncluded(spec.State) {
			continue
		}

		// Get used amount from UsedResources map
		if len(cr.Status.UsedResources) == 0 {
			continue
		}

		// Map ResourceType to resource name and extract used amount
		var resourceName string
		var usedAmount int64
		switch spec.ResourceType {
		case v1alpha1.CommittedResourceTypeMemory:
			resourceName = commitments.ResourceNameRAM(spec.FlavorGroupName)
			memQty, ok := cr.Status.UsedResources["memory"]
			if !ok {
				continue
			}
			// Convert bytes to GiB (1 GiB per commitment unit)
			usedBytes := memQty.Value()
			if _, ok := flavorGroups[spec.FlavorGroupName]; !ok {
				continue
			}
			usedAmount = usedBytes / (1024 * 1024 * 1024)
		case v1alpha1.CommittedResourceTypeCores:
			resourceName = commitments.ResourceNameCores(spec.FlavorGroupName)
			cpuQty, ok := cr.Status.UsedResources["cpu"]
			if !ok {
				continue
			}
			usedAmount = cpuQty.Value()
		default:
			continue
		}

		if usedAmount <= 0 {
			continue
		}

		// Accumulate per AZ
		usage := result[resourceName]
		if usage == nil {
			usage = make(map[string]int64)
		}
		usage[spec.AvailabilityZone] += usedAmount
		result[resourceName] = usage
	}

	return result
}

// isCRStateIncluded checks if a commitment state is in the configured filter.
func (c *QuotaController) isCRStateIncluded(state v1alpha1.CommitmentStatus) bool {
	for _, s := range c.Config.CRStateFilter {
		if s == state {
			return true
		}
	}
	return false
}

// derivePaygUsage computes PaygUsage = TotalUsage - CRUsage (clamped >= 0).
func derivePaygUsage(
	totalUsage map[string]map[string]int64,
	crUsage map[string]map[string]int64,
) map[string]map[string]int64 {

	result := make(map[string]map[string]int64)

	for resourceName, total := range totalUsage {
		payg := make(map[string]int64)
		for az, totalAmount := range total {
			crAmount := int64(0)
			if cr, ok := crUsage[resourceName]; ok {
				if azAmount, ok := cr[az]; ok {
					crAmount = azAmount
				}
			}
			paygAmount := totalAmount - crAmount
			if paygAmount < 0 {
				paygAmount = 0 // Clamp >= 0
			}
			payg[az] = paygAmount
		}
		result[resourceName] = payg
	}

	return result
}

// extractAZSlice extracts the data for a single AZ from a multi-AZ usage map.
// Returns map[resourceName] = value for that AZ only.
func extractAZSlice(usage map[string]map[string]int64, az string) map[string]int64 {
	result := make(map[string]int64)
	for resourceName, azMap := range usage {
		if val, ok := azMap[az]; ok {
			result[resourceName] = val
		}
	}
	return result
}

// expandAZSlice reconstructs a multi-AZ map from a flat per-AZ map.
// Used when reading persisted status (flat) back into the controller's internal format.
func expandAZSlice(flat map[string]int64, az string) map[string]map[string]int64 {
	result := make(map[string]map[string]int64)
	for resourceName, val := range flat {
		result[resourceName] = map[string]int64{az: val}
	}
	return result
}

// updateProjectQuotaStatusWithRetry writes TotalUsage + PaygUsage + LastReconcileAt
// with retry-on-conflict to handle concurrent updates.
// totalUsage and paygUsage are multi-AZ maps; this function extracts the relevant AZ
// slice based on the CRD's Spec.AvailabilityZone.
// If fullReconcile is true, also updates LastFullReconcileAt and ObservedGeneration.
func (c *QuotaController) updateProjectQuotaStatusWithRetry(
	ctx context.Context,
	pqName string,
	totalUsage map[string]map[string]int64,
	paygUsage map[string]map[string]int64,
	fullReconcile bool,
) error {

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Re-fetch fresh copy on each retry
		var pq v1alpha1.ProjectQuota
		if err := c.Get(ctx, client.ObjectKey{Name: pqName}, &pq); err != nil {
			return err
		}

		// Extract only this AZ's data from the multi-AZ maps
		az := pq.Spec.AvailabilityZone
		pq.Status.TotalUsage = extractAZSlice(totalUsage, az)
		pq.Status.PaygUsage = extractAZSlice(paygUsage, az)
		pq.Status.ObservedGeneration = pq.Generation
		now := metav1.Now()
		pq.Status.LastReconcileAt = &now
		if fullReconcile {
			pq.Status.LastFullReconcileAt = &now
		}
		return c.Status().Update(ctx, &pq)
	})
}

// vmResourceUnits computes RAM commitment units (GiB) and cores from a VM's resources.
func vmResourceUnits(resources map[string]resource.Quantity) (ramGiB, cores int64) {
	memQty := resources["memory"]
	serverRAMMiB := memQty.Value() / (1024 * 1024) // bytes to MiB
	ramGiB = serverRAMMiB / 1024                   // MiB to GiB (1 GiB per unit)
	vcpuQty := resources["vcpus"]
	cores = vcpuQty.Value()
	return ramGiB, cores
}

// buildFlavorToGroupMap builds a flavorName → flavorGroupName lookup from flavor groups.
func buildFlavorToGroupMap(flavorGroups map[string]compute.FlavorGroupFeature) map[string]string {
	result := make(map[string]string)
	for groupName, group := range flavorGroups {
		for _, flavor := range group.Flavors {
			result[flavor.Name] = groupName
		}
	}
	return result
}

// incrementUsage increments a usage value in the map.
func incrementUsage(usage map[string]map[string]int64, resourceName, az string, amount int64) {
	u := usage[resourceName]
	if u == nil {
		u = make(map[string]int64)
	}
	u[az] += amount
	usage[resourceName] = u
}

// decrementUsage decrements a usage value in the map (clamp >= 0).
func decrementUsage(usage map[string]map[string]int64, resourceName, az string, amount int64) {
	u := usage[resourceName]
	if u == nil {
		return
	}
	u[az] -= amount
	if u[az] < 0 {
		u[az] = 0
	}
	usage[resourceName] = u
}

// recordUsageMetrics emits Prometheus metrics for all resources in a project.
func (c *QuotaController) recordUsageMetrics(
	projectID string,
	totalUsage map[string]map[string]int64,
	paygUsage map[string]map[string]int64,
	crUsage map[string]map[string]int64,
) {

	for resourceName, total := range totalUsage {
		for az, totalAmount := range total {
			paygAmount := int64(0)
			if payg, ok := paygUsage[resourceName]; ok {
				paygAmount = payg[az]
			}
			crAmount := int64(0)
			if cr, ok := crUsage[resourceName]; ok {
				crAmount = cr[az]
			}
			c.Metrics.RecordUsage(projectID, az, resourceName, totalAmount, paygAmount, crAmount)
		}
	}
}

// ============================================================================
// Predicates & Event Handlers
// ============================================================================

// mapCRToProjectQuota maps a CommittedResource change to the affected ProjectQuota reconcile request.
func (c *QuotaController) mapCRToProjectQuota(_ context.Context, obj client.Object) []reconcile.Request {
	cr, ok := obj.(*v1alpha1.CommittedResource)
	if !ok {
		return nil
	}
	// Map to the per-AZ ProjectQuota for this project + AZ
	crdName := "quota-" + cr.Spec.ProjectID + "-" + cr.Spec.AvailabilityZone
	return []reconcile.Request{
		{NamespacedName: client.ObjectKey{Name: crdName}},
	}
}

// crUsedResourcesChangePredicate triggers on create, delete, and UsedResources changes of a CommittedResource.
func crUsedAmountChangePredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(_ event.CreateEvent) bool { return true },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldCR, ok1 := e.ObjectOld.(*v1alpha1.CommittedResource)
			newCR, ok2 := e.ObjectNew.(*v1alpha1.CommittedResource)
			if !ok1 || !ok2 {
				return false
			}
			// Trigger if UsedResources changed
			if len(oldCR.Status.UsedResources) != len(newCR.Status.UsedResources) {
				return true
			}
			for key, oldQty := range oldCR.Status.UsedResources {
				newQty, ok := newCR.Status.UsedResources[key]
				if !ok || oldQty.Cmp(newQty) != 0 {
					return true
				}
			}
			return false
		},
		DeleteFunc:  func(_ event.DeleteEvent) bool { return true },
		GenericFunc: func(_ event.GenericEvent) bool { return false },
	}
}

// projectQuotaGenerationChangePredicate triggers only when the ProjectQuota's generation changes
// (i.e., spec was modified). This prevents infinite reconcile loops from status-only updates.
func projectQuotaGenerationChangePredicate() predicate.Predicate {
	return predicate.GenerationChangedPredicate{}
}

// hvInstanceChangePredicate always returns true for updates.
// ReconcileHVDiff performs its own set-diff and exits early if there are no
// actual additions/removals. This ensures instance swaps (same count, different IDs)
// are not missed.
func hvInstanceChangePredicate() predicate.TypedPredicate[*hv1.Hypervisor] {
	return predicate.TypedFuncs[*hv1.Hypervisor]{
		CreateFunc: func(_ event.TypedCreateEvent[*hv1.Hypervisor]) bool { return true },
		UpdateFunc: func(_ event.TypedUpdateEvent[*hv1.Hypervisor]) bool {
			return true
		},
		DeleteFunc:  func(_ event.TypedDeleteEvent[*hv1.Hypervisor]) bool { return true },
		GenericFunc: func(_ event.TypedGenericEvent[*hv1.Hypervisor]) bool { return false },
	}
}

// hvInstanceDiffHandler handles HV instance diff events by calling ReconcileHVDiff.
type hvInstanceDiffHandler struct {
	controller *QuotaController
}

func (h *hvInstanceDiffHandler) Create(_ context.Context, _ event.TypedCreateEvent[*hv1.Hypervisor], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	// On create, no diff needed (full reconcile will catch up)
}

func (h *hvInstanceDiffHandler) Update(ctx context.Context, e event.TypedUpdateEvent[*hv1.Hypervisor], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	if err := h.controller.ReconcileHVDiff(ctx, e.ObjectOld, e.ObjectNew); err != nil {
		log.Error(err, "failed to process HV instance diff", "hypervisor", e.ObjectNew.Name)
	}
}

func (h *hvInstanceDiffHandler) Delete(_ context.Context, _ event.TypedDeleteEvent[*hv1.Hypervisor], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	// On delete, full reconcile will correct
}

func (h *hvInstanceDiffHandler) Generic(_ context.Context, _ event.TypedGenericEvent[*hv1.Hypervisor], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	// No-op
}
