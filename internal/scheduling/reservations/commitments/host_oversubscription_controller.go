// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

// HostOversubscriptionController reconciles Hypervisor objects to detect and remediate
// committed-resource oversubscription.
type HostOversubscriptionController struct {
	client.Client
	Conf    ReservationControllerConfig
	Monitor *ReservationControllerMonitor

	// mu protects firstSeen and lastCheck.
	// firstSeen tracks when a violation was first detected per host, used to enforce
	// the grace period before evicting a reservation.
	// lastCheck tracks when a host was last checked, used to rate-limit checks.
	//
	// NOTE: intentionally kept in memory rather than persisted to an HV annotation.
	mu        sync.Mutex
	firstSeen map[string]time.Time
	lastCheck map[string]time.Time
}

func (r *HostOversubscriptionController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := LoggerFromContext(ctx).WithValues("component", "host-oversubscription", "host", req.Name)
	r.mu.Lock()
	defer r.mu.Unlock()
	host := req.Name
	gracePeriod := r.Conf.OversubscriptionGracePeriod.Duration
	if gracePeriod == 0 {
		gracePeriod = 2 * time.Minute
	}
	minCheckInterval := r.Conf.OversubscriptionMinCheckInterval.Duration
	if minCheckInterval == 0 {
		minCheckInterval = 30 * time.Second
	}

	if limited, remaining := r.isRateLimited(host, minCheckInterval); limited {
		logger.V(1).Info("rate limited", "remaining", remaining)
		return ctrl.Result{RequeueAfter: remaining}, nil
	}
	r.recordCheck(host)

	var hv hv1.Hypervisor
	if err := r.Get(ctx, req.NamespacedName, &hv); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var allReservations v1alpha1.ReservationList
	if err := r.List(ctx, &allReservations, client.MatchingFields{reservations.IdxReservationByHost: host}); err != nil {
		return ctrl.Result{}, fmt.Errorf("list reservations for host %s: %w", host, err)
	}

	requeue, err := r.checkOversubscription(ctx, host, allReservations.Items, hv, gracePeriod)
	if err != nil {
		return ctrl.Result{}, err
	}
	if requeue > 0 {
		return ctrl.Result{RequeueAfter: requeue}, nil
	}
	return ctrl.Result{}, nil
}

// checkOversubscription detects and remediates oversubscription for a single host.
// Returns a requeue duration if the grace period is still running, 0 if resolved or no data.
func (r *HostOversubscriptionController) checkOversubscription(
	ctx context.Context,
	host string,
	allReservations []v1alpha1.Reservation,
	hv hv1.Hypervisor,
	gracePeriod time.Duration,
) (time.Duration, error) {

	logger := LoggerFromContext(ctx).WithValues("component", "host-oversubscription", "host", host)
	az := hv.Labels["topology.kubernetes.io/zone"]

	readyCond := meta.FindStatusCondition(hv.Status.Conditions, hv1.ConditionTypeReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionTrue {
		logger.V(1).Info("hypervisor not ready, skipping oversubscription check")
		return 0, nil
	}

	violations := computeViolations(allReservations, hv)
	if violations == nil {
		logger.V(1).Info("no capacity data for host, skipping oversubscription check")
		return 0, nil
	}
	if len(violations) == 0 {
		if !r.firstSeenFor(host).IsZero() {
			logger.Info("host oversubscription resolved")
		}
		r.clearFirstSeen(host)
		r.Monitor.ClearHost(host, az)
		return 0, nil
	}

	r.updateMonitor(host, az, violations)

	firstSeen, isNew := r.getOrSetFirstSeen(host)
	if isNew {
		logger.Info("oversubscription first detected, grace period starting",
			"gracePeriod", gracePeriod, "violations", violations)
	}
	if elapsed := time.Since(firstSeen); elapsed < gracePeriod {
		return gracePeriod - elapsed, nil
	}

	logger.Info("grace period elapsed, evicting slot", "violations", violations)
	if !r.Conf.EnableOversubscriptionReservationEviction {
		logger.Info("eviction disabled — violations detected but eviction skipped (enableOversubscriptionReservationEviction=false)")
		r.resetFirstSeen(host)
		return gracePeriod, nil
	}
	target := selectEvictionTarget(ctx, allReservations, violations)
	if target == nil {
		logger.Info("no evictable slot found — manual intervention required, will retry after grace period")
		r.resetFirstSeen(host)
		return gracePeriod, nil
	}

	if _, err := evictReservation(ctx, r.Client, target); err != nil {
		r.updateMonitor(host, az, violations) // keep monitor current on error
		return 0, err
	}
	r.Monitor.IncSlotsEvicted(az)

	// Recompute violations without the evicted slot to get an accurate post-eviction picture.
	remaining := make([]v1alpha1.Reservation, 0, len(allReservations)-1)
	for i := range allReservations {
		if allReservations[i].Name != target.Name {
			remaining = append(remaining, allReservations[i])
		}
	}
	if postViolations := computeViolations(remaining, hv); len(postViolations) > 0 {
		logger.Info("host still oversubscribed after eviction", "violations", postViolations)
		r.updateMonitor(host, az, postViolations)
	} else {
		logger.Info("host oversubscription resolved after eviction")
		r.Monitor.ClearHost(host, az)
	}

	r.resetFirstSeen(host)
	return gracePeriod, nil
}

func (r *HostOversubscriptionController) updateMonitor(host, az string, violations map[hv1.ResourceName]resource.Quantity) {
	r.Monitor.ClearHost(host, az)
	for rn, excess := range violations {
		r.Monitor.SetOversubscribed(host, az, string(rn), float64(excess.Value()))
	}
}

func (r *HostOversubscriptionController) isRateLimited(host string, minInterval time.Duration) (bool, time.Duration) {
	last := r.lastCheck[host] // safe on nil map — returns zero time
	if !last.IsZero() {
		if elapsed := time.Since(last); elapsed < minInterval {
			return true, minInterval - elapsed
		}
	}
	return false, 0
}

func (r *HostOversubscriptionController) recordCheck(host string) {
	if r.lastCheck == nil {
		r.lastCheck = make(map[string]time.Time)
	}
	r.lastCheck[host] = time.Now()
}

func (r *HostOversubscriptionController) firstSeenFor(host string) time.Time {
	return r.firstSeen[host] // safe on nil map — returns zero time
}

// getOrSetFirstSeen returns the first-seen time for the host, setting it to now if not yet recorded.
// The second return value is true when the entry was just created.
func (r *HostOversubscriptionController) getOrSetFirstSeen(host string) (time.Time, bool) {
	if t := r.firstSeen[host]; !t.IsZero() {
		return t, false
	}
	r.resetFirstSeen(host)
	return r.firstSeen[host], true
}

func (r *HostOversubscriptionController) clearFirstSeen(host string) {
	delete(r.firstSeen, host) // safe on nil map
}

func (r *HostOversubscriptionController) resetFirstSeen(host string) {
	if r.firstSeen == nil {
		r.firstSeen = make(map[string]time.Time)
	}
	r.firstSeen[host] = time.Now()
}

// SetupWithManager wires the controller into the manager.
// Primary subject: Hypervisor — one reconcile per host, requeue is stable regardless of
// which reservation triggered it.
// Secondary watch: all Reservations mapped to their host via Status.Host — any reservation
// type can affect host capacity (failover slots, VM allocations, etc.), so we watch all.
func (r *HostOversubscriptionController) SetupWithManager(mgr ctrl.Manager, mcl *multicluster.Client) error {
	if err := reservations.IndexReservationByHost(context.Background(), mcl); err != nil {
		return fmt.Errorf("failed to set up reservation by host index: %w", err)
	}

	enqueueHost := func(res *v1alpha1.Reservation, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		if host := res.Status.Host; host != "" {
			q.Add(reconcile.Request{NamespacedName: client.ObjectKey{Name: host}})
		}
	}

	reservationToHost := handler.Funcs{
		CreateFunc: func(_ context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enqueueHost(e.Object.(*v1alpha1.Reservation), q)
		},
		UpdateFunc: func(_ context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enqueueHost(e.ObjectOld.(*v1alpha1.Reservation), q) // old host — reservation may have moved away
			enqueueHost(e.ObjectNew.(*v1alpha1.Reservation), q)
		},
		DeleteFunc: func(_ context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enqueueHost(e.Object.(*v1alpha1.Reservation), q)
		},
	}

	bldr := multicluster.BuildController(mcl, mgr)
	var err error
	bldr, err = bldr.WatchesMulticluster(
		&hv1.Hypervisor{},
		&handler.EnqueueRequestForObject{},
		hvCapacityChangePredicate,
	)
	if err != nil {
		return err
	}
	bldr, err = bldr.WatchesMulticluster(
		&v1alpha1.Reservation{},
		reservationToHost,
	)
	if err != nil {
		return err
	}
	return bldr.Named("host-oversubscription").Complete(r)
}

// computeViolations returns the per-resource excess (positive quantity) for any resource where
// the host is over-subscribed. Returns nil if capacity data is unavailable, empty map if no violation.
func computeViolations(allReservations []v1alpha1.Reservation, hv hv1.Hypervisor) map[hv1.ResourceName]resource.Quantity {
	free := reservations.HostFreeCapacity(allReservations, hv)
	if free == nil {
		return nil
	}
	zero := resource.MustParse("0")
	violations := make(map[hv1.ResourceName]resource.Quantity)
	for rn, f := range free {
		if f.Cmp(zero) < 0 {
			excess := f.DeepCopy()
			excess.Neg()
			violations[rn] = excess
		}
	}
	return violations
}

// selectEvictionTarget picks the least-disruptive slot to evict: prefers the smallest
// unallocated slot (no running VMs, easiest to re-place), falling back to the most-idle
// allocated slot when no unallocated candidates exist.
func selectEvictionTarget(
	ctx context.Context,
	allReservations []v1alpha1.Reservation,
	violations map[hv1.ResourceName]resource.Quantity,
) *v1alpha1.Reservation {

	logger := LoggerFromContext(ctx).WithValues("component", "host-oversubscription")
	var selectedRes *v1alpha1.Reservation

	var unallocated, allocated []*v1alpha1.Reservation
	for i := range allReservations {
		res := &allReservations[i]
		if res.Spec.Type != v1alpha1.ReservationTypeCommittedResource || res.Spec.CommittedResourceReservation == nil {
			continue
		}
		if res.Spec.TargetHost == "" {
			continue // already evicted, pending status cleanup
		}
		if len(res.Spec.CommittedResourceReservation.Allocations) == 0 {
			unallocated = append(unallocated, res)
		} else {
			allocated = append(allocated, res)
		}
	}

	// Sort unallocated by memory asc, then CPU asc — smallest slots are easiest to re-place.
	sort.SliceStable(unallocated, func(i, j int) bool {
		ri, rj := unallocated[i].Spec.Resources, unallocated[j].Spec.Resources
		mi, mj := ri[hv1.ResourceMemory], rj[hv1.ResourceMemory]
		if c := mi.Cmp(mj); c != 0 {
			return c < 0
		}
		ci, cj := ri[hv1.ResourceCPU], rj[hv1.ResourceCPU]
		return ci.Cmp(cj) < 0
	})

	if len(unallocated) > 0 {
		selectedRes = unallocated[0]
	}

	if selectedRes == nil && len(allocated) > 0 {
		type allocatedEntry struct {
			res     *v1alpha1.Reservation
			buckets map[hv1.ResourceName]int64
		}
		entries := make([]allocatedEntry, len(allocated))
		for i, res := range allocated {
			unusedCap := reservations.UnusedReservationCapacity(res, false)
			buckets := make(map[hv1.ResourceName]int64)
			for rn, total := range res.Spec.Resources {
				if t := total.Value(); t != 0 {
					unused := unusedCap[rn]
					buckets[rn] = unused.Value() * 10 / t
				}
			}
			entries[i] = allocatedEntry{res: res, buckets: buckets}
		}
		// Sort: 1st by unused mem ratio desc, 2nd by unused CPU ratio desc, 3rd by total mem asc.
		sort.SliceStable(entries, func(i, j int) bool {
			if d := entries[i].buckets[hv1.ResourceMemory] - entries[j].buckets[hv1.ResourceMemory]; d != 0 {
				return d > 0
			}
			if d := entries[i].buckets[hv1.ResourceCPU] - entries[j].buckets[hv1.ResourceCPU]; d != 0 {
				return d > 0
			}
			mi, mj := entries[i].res.Spec.Resources[hv1.ResourceMemory], entries[j].res.Spec.Resources[hv1.ResourceMemory]
			return mi.Cmp(mj) < 0
		})
		selectedRes = entries[0].res
	}

	if selectedRes == nil {
		logger.Info("no eviction target found")
		return nil
	}
	logger.Info("eviction target selected",
		"reservation", selectedRes.Name,
		"total unallocated slots", len(unallocated),
		"total allocated slots", len(allocated),
		"slot resources", selectedRes.Spec.Resources,
		"violations", violations,
	)
	return selectedRes
}

// evictReservation clears Spec.TargetHost and Spec.Allocations, and returns the resources
// freed (full Spec.Resources — the slot is fully evicted). Status cleanup is left to the reconcile loop
func evictReservation(
	ctx context.Context,
	c client.Client,
	res *v1alpha1.Reservation,
) (map[hv1.ResourceName]resource.Quantity, error) {

	logger := LoggerFromContext(ctx).WithValues("component", "host-oversubscription", "reservation", res.Name)

	freed := reservations.UnusedReservationCapacity(res, true)

	old := res.DeepCopy()
	res.Spec.TargetHost = ""
	if res.Spec.CommittedResourceReservation != nil {
		res.Spec.CommittedResourceReservation.Allocations = nil
	}
	logger.Info("evicting reservation",
		"freed", freed,
		"previous host", old.Spec.TargetHost,
		"resources", old.Spec.Resources)
	if err := c.Patch(ctx, res, client.MergeFrom(old)); err != nil {
		logger.Error(err, "failed to patch reservation", "reservation", res.Name)
		return nil, fmt.Errorf("failed to patch reservation %s: %w", res.Name, err)
	}
	return freed, nil
}
