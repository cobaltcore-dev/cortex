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
//   - A periodic calculation loop (Start -> loop, both in controller.go)
//     recomputes the desired set of failover host reservations from the current
//     cluster state on a fixed interval, and stores it as desiredReservations.
//   - An event-driven reconciler (Reconcile, below) brings the actual
//     reservations in the cluster in line with that desired set, creating,
//     updating and pruning Reservation objects.
//
// The loop never writes Reservation objects itself; it only recomputes the
// desired state and nudges the reconciler. Keeping the expensive, external-call
// heavy computation out of the reconcile path means reconciles stay cheap and
// idempotent.
//
// # The calculation
//
// The calculation half is a ReservationGenerator (generation.go): loop calls
// Generate each pass, swaps its result in as the desired state, and wakes the
// reconciler. The production generator runs a four-stage pipeline (Seeder ->
// PathFinder -> PartitionBuilder -> ReservationPlanner); see pipelineGenerator
// in generation.go for the end-to-end flow. The interface lets the whole
// generation strategy be swapped without touching the controller's plumbing.
package failoverhost

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
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

	// Monitor records Prometheus metrics for the calculation loop and the
	// reconciler. It may be nil (e.g. in tests); every call site nil-guards it.
	Monitor *Monitor

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

	// generator computes the desired reservations each pass (the swappable
	// producer half of the controller). It is defaulted to the production
	// pipelineGenerator in SetupWithManager but can be set beforehand to
	// substitute a fake in tests. See generation.go.
	generator ReservationGenerator
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

	// Before acting on a desired reservation, confirm the hypervisor it targets
	// still exists and still has the capacity the reservation reserves (a failover
	// host reservation blocks a host's full capacity, so a drift means we would
	// under- or over-reserve). The reservation's resources are the host's capacity
	// overlaid with its effective capacity (see the planner), so rebuild that same
	// view and compare it to the reservation. A missing hypervisor or a mismatch
	// returns an error so the reconcile is retried with exponential backoff, giving
	// the hypervisor CRD and the calculation loop time to settle.
	if desiredExists {
		hv := &hv1.Hypervisor{}
		if err := c.Get(ctx, client.ObjectKey{Name: desired.Spec.TargetHost}, hv); err != nil {
			log.Error(err, "failed to get hypervisor for reservation", "host", desired.Spec.TargetHost)
			c.Monitor.RecordReservationError("hypervisor")
			return ctrl.Result{}, err
		}
		capacity := map[hv1.ResourceName]resource.Quantity{}
		maps.Copy(capacity, hv.Status.Capacity)
		maps.Copy(capacity, hv.Status.EffectiveCapacity)
		// equality.Semantic compares resource.Quantity by value, unlike
		// reflect.DeepEqual which trips over its unexported cached state.
		if !equality.Semantic.DeepEqual(capacity, desired.Spec.Resources) {
			log.Error(nil, "hypervisor capacity does not match reservation, retrying",
				"host", desired.Spec.TargetHost, "hypervisorCapacity", capacity, "reservationResources", desired.Spec.Resources)
			c.Monitor.RecordReservationError("hypervisor")
			return ctrl.Result{}, errors.New("hypervisor capacity does not match reservation")
		}
	}

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
			c.Monitor.RecordReservationError("delete")
			return ctrl.Result{}, err
		}
		log.Info("deleted stale failover host reservation", "host", actual.Spec.TargetHost)
		c.Monitor.RecordReservationDeleted()
		return ctrl.Result{}, nil

	case desiredExists && !actualExists:
		// The reservation is desired but not yet present, create it.
		toCreate := desired.DeepCopy()
		if err := c.Create(ctx, toCreate); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return ctrl.Result{}, nil
			}
			log.Error(err, "failed to create failover host reservation")
			c.Monitor.RecordReservationError("create")
			return ctrl.Result{}, err
		}
		log.Info("created failover host reservation", "host", desired.Spec.TargetHost)
		c.Monitor.RecordReservationCreated()
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
			c.Monitor.RecordReservationError("update")
			return ctrl.Result{}, err
		}
		log.Info("updated failover host reservation", "host", desired.Spec.TargetHost)
		c.Monitor.RecordReservationUpdated()
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
// It also defaults the generator to the production pipelineGenerator. Tests may
// set it beforehand to substitute a fake (e.g. a path finder pointed at a stub
// scheduler); it is only defaulted here when nil.
func (c *Controller) SetupWithManager(mgr ctrl.Manager) error {
	if mgr == nil {
		return errors.New("manager cannot be nil")
	}
	// Default the generator. Tests may set it before calling Setup to substitute
	// a fake; here we wire the production pipeline stages.
	if c.generator == nil {
		c.generator = &pipelineGenerator{
			Client:  c.Client,
			Monitor: c.Monitor,
			seeder:  &seeder{k8s: c.Client},
			pathFinder: &evacuationPathFinder{
				// Timeout so a hung scheduler can't stall the calculation loop.
				httpClient:   &http.Client{Timeout: 30 * time.Second},
				schedulerURL: c.Config.ExternalSchedulerURL,
				monitor:      c.Monitor,
			},
			partitionBuilder:   cliquePartitionBuilder{},
			reservationPlanner: ratioReservationPlanner{ratio: c.Config.Ratio, minimum: c.Config.Minimum},
		}
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

// loop runs one calculation pass: it asks the generator for the desired failover
// host reservations, swaps them in as the new desired state, and wakes the
// reconciler. The heavy lifting (snapshotting the cluster and running the
// pipeline) lives in the generator; see generation.go.
//
// The desired map is swapped in wholesale under the lock Reconcile reads it with,
// so Reconcile never sees a half-built state. Replacing (rather than mutating)
// the map is what lets Reconcile prune reservations that are no longer planned:
// anything absent from the new map but still present in the cluster is detected
// as stale.
//
// A generator that returns ok=false aborts the pass without touching the desired
// state: a partial recalculation is worse than a slightly stale one, as it could
// drop reservations that are still valid.
func (c *Controller) loop(ctx context.Context) {
	desired, topo, ok := c.generator.Generate(ctx)
	if !ok {
		return // the generator already logged the reason and recorded the abort.
	}
	c.desiredReservationsMu.Lock()
	c.desiredReservations = desired
	c.desiredReservationsMu.Unlock()
	c.publish(ctx, topo, desired)
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
