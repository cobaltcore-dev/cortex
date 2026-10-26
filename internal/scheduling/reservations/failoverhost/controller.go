// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

// Package failoverhost keeps a fleet of OpenStack Nova compute hosts ready to
// absorb the workload of a failed host.
//
// # What a failover host reservation is
//
// When a hypervisor fails, Nova has to evacuate every virtual machine running
// on it onto other hosts. For that to succeed quickly and predictably there
// must be somewhere to put those vms. This package keeps a fraction of every
// group of interchangeable hosts empty and reserved, so a whole host's workload
// can be gang-evacuated onto a reserved "failover host" in one move. A failover
// host reservation (v1alpha1.Reservation of type FailoverHostReservation)
// blocks a target host from receiving ordinary placements so it stays available
// for this purpose. Its full lifecycle (Designated -> Active -> In Use, plus the
// Failed and Revoked/Used terminal states) is documented on the
// ReservationTypeFailoverHost constant in the v1alpha1 package.
//
// # Partitions
//
// Hosts are not interchangeable across the whole cloud: a HANA host, a
// general-purpose host and an external-customer host can generally only ever
// fail over within their own kind, because their workloads carry scheduling
// constraints (flavors, image properties, aggregates) that only matching hosts
// satisfy. We therefore group hosts into failover partitions: maximal sets of
// hosts within which every host can be evacuated onto every other host. We do
// not configure these groups by hand; we discover them from the scheduler's own
// placement decisions, so the grouping always reflects the live constraints.
//
// # The two halves of the controller
//
// The controller has a producer and a consumer that communicate through the
// desiredReservations map:
//
//   - A periodic calculation loop (Start -> loop, see loop.go) recomputes the
//     desired set of failover host reservations from the current cluster state
//     on a fixed interval, and stores it as desiredReservations.
//   - An event-driven reconciler (Reconcile, below) brings the actual
//     reservations in the cluster in line with that desired set, creating,
//     updating and pruning Reservation objects.
//
// The loop never writes Reservation objects itself; it only recomputes the
// desired state and nudges the reconciler. Keeping the expensive, external-call
// heavy computation out of the reconcile path means reconciles stay cheap and
// idempotent.
//
// # The calculation pipeline
//
// Each pass of the loop runs a three-stage pipeline, each stage a small
// interface so it can be understood, tested and swapped in isolation:
//
//  1. PathFinder (paths.go) turns the cluster into the directed edges of a
//     failover graph: source -> target means "every vm on source can be
//     evacuated onto target, and target is big enough to hold them all at once".
//  2. PartitionBuilder (partitions.go) clusters those edges into partitions
//     (maximal sets of mutually-evacuable hosts).
//  3. ReservationPlanner (reservations.go) decides, per partition, which hosts
//     become failover targets, honouring the configured ratio and minimum.
//
// loop.go wires these together and documents the end-to-end flow.
package failoverhost

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	vmv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// Config is the top-level config section this controller reads from the shared
// cortex conf.json. The controller's own settings are nested under failover.hosts
// so they sit alongside the (flavor-based) failover controller's settings without
// colliding with the flat, top-level keys read from the same file.
type Config struct {
	Failover struct {
		Hosts ControllerConfig `json:"hosts"`
	} `json:"failover"`
}

// ControllerConfig contains configuration for failover host reservations.
type ControllerConfig struct {
	// Ratio is a global ratio that determines how many failover hosts inside
	// a partition are created. For example, a ratio of 0.1 means that
	// if a partition consists of 10 hosts, 1 failover host will be created.
	// The number of resulting failover hosts in a partition will be rounded
	// down. This means if there are not sufficient hosts inside a partition,
	// it is possible that no failover hosts will be created.
	Ratio float64 `json:"ratio"`

	// Minimum is the minimum number of failover hosts that should be created
	// in a partition, regardless of the ratio. This ensures that even small
	// partitions have at least a certain number of failover hosts.
	Minimum int `json:"minimum"`

	// GenerationInterval is the interval within which failover partitions are
	// recalculated and failover host reservations are updated accordingly.
	GenerationInterval metav1.Duration `json:"generationInterval"`

	// ExternalSchedulerURL is the URL of the external scheduler service that
	// will be used to request failover host placements.
	ExternalSchedulerURL string `json:"externalSchedulerURL"`
}

// Controller keeps the cluster's failover host reservations in sync with the
// current cluster state. It has two cooperating halves (see the package doc for
// the bigger picture):
//
//   - a periodic calculation loop (Start -> loop) that recomputes the desired
//     reservations and stores them in desiredReservations, and
//   - the Reconcile method that drives the actual Reservation objects toward
//     that desired set.
//
// The loop is the single writer of desiredReservations and Reconcile the single
// reader, both synchronised by desiredReservationsMu.
type Controller struct {
	client.Client // k8s client

	Config ControllerConfig

	// desiredReservations is the desired state computed by the latest loop
	// pass: the failover host reservations that should exist right now, keyed
	// by their object reference (name only; reservations are cluster-scoped).
	// Reconcile compares the actual cluster against this map. It is replaced
	// wholesale each pass rather than mutated in place.
	desiredReservations map[corev1.ObjectReference]v1alpha1.Reservation

	// desiredReservationsMu guards desiredReservations against concurrent access
	// by the calculation loop (writer) and Reconcile (reader).
	desiredReservationsMu sync.Mutex

	// reconcileCh carries a raw wake-up signal from the loop to the reconciler.
	// After a pass the loop sends one event per reservation that is either
	// desired or still present, so the reconciler re-examines each against the
	// freshly computed desired state. The event payload only identifies the
	// reservation; the reconciler reads desiredReservations itself.
	reconcileCh chan event.GenericEvent

	// The three stages of the calculation pipeline run by loop. They are
	// defaulted to the production implementations in SetupWithManager but can be
	// set beforehand to substitute fakes in tests. See loop for how they fit
	// together, and paths.go / partitions.go / reservations.go for each stage.
	pathFinder         PathFinder
	partitionBuilder   PartitionBuilder
	reservationPlanner ReservationPlanner
}

// Reconcile drives a single failover host reservation toward the desired state
// computed by the calculation loop. It is triggered by the loop (via
// reconcileCh) for every reservation that is either desired or still present, so
// between them the two cover create, update, delete and no-op:
//
//   - desired, not present     -> create it.
//   - not desired, present     -> prune it (it no longer matches any partition).
//   - desired and present      -> update it if the spec/labels drifted.
//   - neither desired nor present, or already in sync -> nothing to do.
//
// It is level-triggered and idempotent: it reads the current actual and desired
// state on every call and makes at most one write, so redundant events and
// retries are harmless.
func (c *Controller) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithName("failoverhost-controller-reconcile").
		WithValues("reservationName", req.Name)

	// Fetch the actual reservation from the cluster, if it exists.
	actual := &v1alpha1.Reservation{}
	actualExists := true
	if err := c.Get(ctx, req.NamespacedName, actual); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "failed to get reservation")
			return ctrl.Result{}, err
		}
		actualExists = false
	}

	// Fetch the desired reservation produced by the partitioning loop.
	c.desiredReservationsMu.Lock()
	desired, desiredExists := c.desiredReservations[corev1.ObjectReference{Name: req.Name}]
	c.desiredReservationsMu.Unlock()

	switch {
	case !desiredExists && !actualExists:
		// Nothing to do, both the desired and actual state agree it is gone.
		log.V(1).Info("reservation neither desired nor present, nothing to do")
		return ctrl.Result{}, nil

	case !desiredExists && actualExists:
		// The reservation is no longer part of any failover partition, prune it.
		if err := c.Delete(ctx, actual); err != nil {
			if apierrors.IsNotFound(err) {
				return ctrl.Result{}, nil
			}
			log.Error(err, "failed to delete stale reservation")
			return ctrl.Result{}, err
		}
		log.Info("deleted stale failover host reservation", "host", actual.Spec.TargetHost)
		return ctrl.Result{}, nil

	case desiredExists && !actualExists:
		// The reservation is desired but not yet present, create it.
		toCreate := desired.DeepCopy()
		if err := c.Create(ctx, toCreate); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return ctrl.Result{}, nil
			}
			log.Error(err, "failed to create failover host reservation")
			return ctrl.Result{}, err
		}
		log.Info("created failover host reservation", "host", desired.Spec.TargetHost)
		return ctrl.Result{}, nil

	default:
		// Both exist. Update the actual spec if it drifted from the desired one.
		// equality.Semantic handles resource.Quantity correctly, unlike
		// reflect.DeepEqual which trips over its unexported cached state.
		if equality.Semantic.DeepEqual(actual.Spec, desired.Spec) &&
			maps.Equal(actual.Labels, desired.Labels) {
			log.V(1).Info("reservation already matches desired state")
			return ctrl.Result{}, nil
		}
		actual.Spec = desired.Spec
		// Replace labels outright rather than merging, so a label that is no
		// longer desired is removed — a merge would keep it and make every pass
		// re-detect drift and rewrite the object forever.
		actual.Labels = maps.Clone(desired.Labels)
		if err := c.Update(ctx, actual); err != nil {
			log.Error(err, "failed to update failover host reservation")
			return ctrl.Result{}, err
		}
		log.Info("updated failover host reservation", "host", desired.Spec.TargetHost)
		return ctrl.Result{}, nil
	}
}

// Start runs the periodic calculation loop until ctx is cancelled.
//
// It implements manager.Runnable so the manager owns its lifecycle (it is
// registered via mgr.Add in SetupWithManager). Start first validates the config
// — a misconfigured ratio, minimum or interval is a programming/deployment error
// that should fail fast rather than silently produce no reservations — then runs
// one pass immediately (so reservations exist without waiting a full interval)
// and re-runs loop on every tick. On shutdown it closes reconcileCh so the
// reconciler's event source drains cleanly.
func (c *Controller) Start(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("failoverhost-controller-startup")
	log.Info("starting failover host controller")

	// Validate the config
	interval := c.Config.GenerationInterval.Duration
	if interval <= 0 || interval < time.Minute {
		return errors.New("generation interval too low or not set")
	}
	if c.Config.Ratio <= 0 || c.Config.Ratio > 1 {
		return errors.New("failover ratio must be between 0 and 1")
	}
	if c.Config.Minimum <= 0 {
		return errors.New("minimum must be greater than 0")
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer close(c.reconcileCh)
	// Run the routine once before entering the ticker loop.
	c.loop(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			c.loop(ctx)
		}
	}
}

// SetupWithManager wires both halves of the controller into the manager:
//
//   - It registers the Controller as a Runnable (mgr.Add) so the manager starts
//     and stops the periodic calculation loop (Start) alongside the controller.
//   - It registers the reconciler with a channel event source, so the loop can
//     enqueue reconcile requests by sending on reconcileCh.
//
// It also defaults the three pipeline stages to their production
// implementations. Tests may set any of them beforehand to substitute a fake
// (e.g. a path finder pointed at a stub scheduler); only the nil ones are
// defaulted here.
func (c *Controller) SetupWithManager(mgr ctrl.Manager) error {
	if mgr == nil {
		return errors.New("manager cannot be nil")
	}
	// Default the pipeline stages. Tests may set these before calling Setup to
	// substitute fakes; here we wire the production implementations.
	if c.pathFinder == nil {
		c.pathFinder = &evacuationPathFinder{
			// Timeout so a hung scheduler can't stall the calculation loop.
			httpClient:   &http.Client{Timeout: 30 * time.Second},
			schedulerURL: c.Config.ExternalSchedulerURL,
		}
	}
	if c.partitionBuilder == nil {
		c.partitionBuilder = cliquePartitionBuilder{}
	}
	if c.reservationPlanner == nil {
		c.reservationPlanner = ratioReservationPlanner{ratio: c.Config.Ratio, minimum: c.Config.Minimum}
	}

	c.reconcileCh = make(chan event.GenericEvent)
	src := source.Channel(c.reconcileCh, &handler.EnqueueRequestForObject{})

	// Register the loop so the manager starts and stops it alongside the controller.
	if err := mgr.Add(c); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named("failoverhost-controller").
		WatchesRawSource(src).
		Complete(c)
}

// topology is the host/vm layout a single calculation pass reasons over: which
// vms sit on which hypervisor, and which failover host reservations exist right
// now. It is a consistent snapshot built once per loop by collect, so every
// pipeline stage reasons over the same view of the cluster.
type topology struct {
	// hypervisors indexes every known hypervisor by its name.
	hypervisors map[string]hv1.Hypervisor
	// vmsByHost groups the placed virtual machines by the hypervisor they run
	// on. Unplaced vms are excluded, so every key is a populated host.
	vmsByHost map[string][]vmv1alpha1.VirtualMachine
	// existing holds the failover host reservations currently in the cluster,
	// keyed by their object reference. The planner prefers to keep these stable.
	existing map[corev1.ObjectReference]v1alpha1.Reservation
}

// loop runs one calculation pass of the failover host pipeline:
//
//  1. collect   — snapshot the cluster into a consistent topology.
//  2. FindPaths — for each occupied host, find the hosts its whole workload
//     could be gang-evacuated onto (the directed edges of the failover graph).
//  3. BuildPartitions — cluster those edges into failover partitions: maximal
//     sets of hosts that can all fail over onto one another.
//  4. PlanReservations — per partition, pick which hosts become failover
//     targets, honouring the configured ratio/minimum and keeping existing
//     reservations stable.
//  5. publish   — store the result as the desired state and wake the reconciler.
//
// Any error or inconsistency aborts the whole pass without touching the desired
// state: a partial recalculation is worse than a slightly stale one, as it could
// drop reservations that are still valid.
func (c *Controller) loop(ctx context.Context) {
	log := logf.FromContext(ctx).WithName("failoverhost-controller-calculation-loop")
	log.Info("recalculating failover host reservations")

	topo, ok := c.collect(ctx)
	if !ok {
		return // collect already logged the reason.
	}

	paths, err := c.pathFinder.FindPaths(ctx, topo)
	if err != nil {
		log.Error(err, "failed to find failover paths")
		return
	}

	// Cluster the failover edges into partitions. See partitions.go for why a
	// partition is a maximal clique of mutually-evacuable hosts, and how empty
	// hosts are recruited into the partition that can reach them.
	partitions := c.partitionBuilder.BuildPartitions(paths)
	log.V(1).Info("clustered failover paths into partitions",
		"paths", len(paths), "partitions", len(partitions))
	labels := characterizePartitions(partitions)
	for i, partition := range partitions {
		// Describe what sets this partition apart from the others: the shared
		// fields no other partition shares. See characterizePartitions.
		log.V(1).Info("characterized failover partition",
			"partition", i, "partitionSize", len(partition),
			"distinctBy", labels[i])
	}

	// Plan the desired reservations per partition and collect them into one map.
	// We then swap that map in wholesale as the new desired state, under the
	// lock Reconcile reads it with, so Reconcile never sees a half-built state.
	// Replacing (rather than mutating) the map is what lets Reconcile prune
	// reservations that are no longer planned: anything absent from the new map
	// but still present in the cluster is detected as stale.
	desired := make(map[corev1.ObjectReference]v1alpha1.Reservation)
	for i, partition := range partitions {
		planned := c.reservationPlanner.PlanReservations(topo, partition, labels[i])
		for _, reservation := range planned {
			desired[corev1.ObjectReference{Name: reservation.Name}] = reservation
		}
		log.V(1).Info("planned failover hosts for partition",
			"partitionSize", len(partition), "planned", len(planned))
	}
	c.desiredReservationsMu.Lock()
	c.desiredReservations = desired
	c.desiredReservationsMu.Unlock()
	log.Info("generated desired failover host reservations",
		"partitions", len(partitions), "desiredReservations", len(desired),
		"existingReservations", len(topo.existing))

	c.publish(ctx, topo, desired)
}

// collect snapshots the current cluster into a topology. It returns ok=false if
// the snapshot is inconsistent (e.g. a vm sits on an unknown hypervisor, or a
// hypervisor lacks an availability zone), in which case the loop must abort
// rather than compute on a partial view.
func (c *Controller) collect(ctx context.Context) (topology, bool) {
	log := logf.FromContext(ctx).WithName("failoverhost-controller-calculation-loop")

	hvs := &hv1.HypervisorList{}
	if err := c.List(ctx, hvs); err != nil {
		log.Error(err, "failed to list hypervisors")
		return topology{}, false
	}
	hvsByName := make(map[string]hv1.Hypervisor)
	for i := range hvs.Items {
		hvsByName[hvs.Items[i].Name] = hvs.Items[i]
	}

	vms := &vmv1alpha1.VirtualMachineList{}
	if err := c.List(ctx, vms); err != nil {
		log.Error(err, "failed to list virtual machines")
		return topology{}, false
	}
	vmsByHost := make(map[string][]vmv1alpha1.VirtualMachine)
	for i := range vms.Items {
		vm := vms.Items[i]
		// Skip vms that Nova hasn't placed on a host yet. They can't contribute
		// a failover path and would otherwise nil-panic on Status.Nova below.
		if vm.Status.Nova == nil || vm.Status.Nova.Host == "" {
			log.V(1).Info("skipping unplaced vm", "vm", vm.Name)
			continue
		}
		vmsByHost[vm.Status.Nova.Host] = append(vmsByHost[vm.Status.Nova.Host], vm)
	}
	log.V(1).Info("listed cluster state", "hypervisors", len(hvsByName), "virtualMachines", len(vms.Items))

	// Ensure that all vms are on known hypervisors with an availability zone,
	// otherwise abort to avoid calculating on an inconsistent set of hosts.
	for host := range vmsByHost {
		hv, ok := hvsByName[host]
		if !ok {
			log.Error(nil, "vm is on unknown hypervisor, aborting", "host", host)
			return topology{}, false
		}
		if _, ok := hv.Labels[corev1.LabelTopologyZone]; !ok {
			log.Error(nil, "hypervisor has no availability zone, aborting", "host", host)
			return topology{}, false
		}
	}

	allReservations := &v1alpha1.ReservationList{}
	if err := c.List(ctx, allReservations); err != nil {
		log.Error(err, "failed to list existing failover host reservations")
		return topology{}, false
	}
	existing := make(map[corev1.ObjectReference]v1alpha1.Reservation)
	for _, reservation := range allReservations.Items {
		if reservation.Spec.Type == v1alpha1.ReservationTypeFailoverHost {
			existing[corev1.ObjectReference{Name: reservation.Name}] = reservation
		}
	}

	return topology{hypervisors: hvsByName, vmsByHost: vmsByHost, existing: existing}, true
}

// publish wakes the reconciler for every reservation that is either desired now
// or already present in the cluster. The union matters: desired-but-absent
// reservations need creating, and present-but-no-longer-desired reservations
// need pruning, so both sets must be re-examined. Sending is a raw signal — the
// reconciler re-reads the desired state itself — so the event payload only needs
// to identify the reservation. Sends block on reconcileCh; a cancelled ctx (the
// manager shutting down) unblocks and returns.
func (c *Controller) publish(ctx context.Context, topo topology, desired map[corev1.ObjectReference]v1alpha1.Reservation) {
	toReconcile := make(map[corev1.ObjectReference]v1alpha1.Reservation)
	for k, v := range topo.existing {
		toReconcile[k] = v
	}
	for k, v := range desired {
		toReconcile[k] = v
	}
	for _, reservation := range toReconcile {
		evt := event.GenericEvent{Object: &reservation}
		select {
		case c.reconcileCh <- evt:
		case <-ctx.Done():
			return
		}
	}
}
