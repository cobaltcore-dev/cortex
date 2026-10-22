// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	vmv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// Controller reconciles failover host reservations based on generated partitions.
// It runs a periodic partitioning algorithm out of which desired reservations
// are derived and used to update the actual failover host reservations in the
// cluster.
type Controller struct {
	client.Client

	Config FailoverHostConfig

	// desiredReservations contains the desired state of failover host
	// reservations that has been generated using the partitioning algorithm,
	// by their namespaced name.
	desiredReservations map[corev1.ObjectReference]v1alpha1.Reservation

	// desiredReservationsMu protects access to the desiredReservations map.
	desiredReservationsMu sync.Mutex

	// reconcileCh is a channel used to signal updates to the
	// desiredReservations map. This channel is used as a raw signal for
	// our controller to trigger a reconciliation on each generated reservation
	// such that it can be checked against the actual cluster state.
	reconcileCh chan event.GenericEvent
}

// Reconcile reconciles a failover host reservation by comparing the desired state
// derived from the generated reservations with the actual state in the cluster,
// and making necessary updates to ensure consistency.
func (c *Controller) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = logf.FromContext(ctx).WithName("failoverhost-controller-reconcile")

	// TODO:
	// - Retrieve the reservation from the cluster using the request's namespaced name.
	// - Retrieve the reservation inside the generatedReservations map using the request's namespaced name.
	// - Compare the desired state (generatedReservations) map with the actual state from the cluster.
	// - Update the actual reservation in the cluster if it differs from the desired state.
	// - Return the appropriate result and error.
	return ctrl.Result{}, nil
}

// Start runs the periodic calculation loop until ctx is cancelled. It implements
// manager.Runnable so the manager owns its lifecycle; it is registered via
// mgr.Add in SetupWithManager.
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

// fetchEvacuationCandidates retrieves the possible evacuation targets for a
// given virtual machine.
func (c *Controller) fetchEvacuationCandidates(ctx context.Context, vm vmv1alpha1.VirtualMachine) ([]hv1.Hypervisor, error) {
	return nil, nil
}

// intersection returns the intersection of two sets of hypervisors.
// If the first set is empty, it returns the second set.
func (c *Controller) intersection(a, b []hv1.Hypervisor) []hv1.Hypervisor {
	if len(a) == 0 {
		return b
	}
	intersection := []hv1.Hypervisor{}
	for _, ha := range a {
		for _, hb := range b {
			if ha.Name == hb.Name {
				intersection = append(intersection, ha)
				break
			}
		}
	}
	return intersection
}

// targetLargeEnough checks if the given target hypervisor has enough capacity
// to accommodate the failover from the source hypervisor.
func (c *Controller) targetLargeEnough(source, target hv1.Hypervisor) bool {
	return true // TODO
}

// clusterFailoverPaths clusters the given failover paths into fully connected
// subgraphs, representing failover partitions. Each partition contains a set of
// hypervisors that are mutually reachable via failover paths.
func (c *Controller) clusterFailoverPaths(paths [][2]hv1.Hypervisor) ([][]hv1.Hypervisor, error) {
	return nil, nil // TODO
}

// findExistingReservations returns the set of hypervisors within the given partition
// that already have failover host reservations.
func (c *Controller) findExistingFailoverHostReservations(partition []hv1.Hypervisor) []v1alpha1.Reservation {
	return nil // TODO
}

// loop runs the main calculation loop for failover host reservations.
// It lists hypervisors and virtual machines, determines evacuation targets,
// applies constraints, and updates the desired state accordingly.
func (c *Controller) loop(ctx context.Context) {
	log := logf.FromContext(ctx).WithName("failoverhost-controller-calculation-loop")

	hvs := &hv1.HypervisorList{}
	if err := c.List(ctx, hvs); err != nil {
		log.Error(err, "failed to list hypervisors")
		return
	}
	hvsByName := make(map[string]hv1.Hypervisor)
	for i := range hvs.Items {
		hv := hvs.Items[i]
		hvsByName[hv.Name] = hv
	}
	vms := &vmv1alpha1.VirtualMachineList{}
	if err := c.List(ctx, vms); err != nil {
		log.Error(err, "failed to list virtual machines")
		return
	}
	vmsByHost := make(map[string][]vmv1alpha1.VirtualMachine)
	for i := range vms.Items {
		vm := vms.Items[i]
		vmsByHost[vm.Status.Nova.Host] = append(vmsByHost[vm.Status.Nova.Host], vm)
	}
	// Ensure that all vms are on known hypervisors, otherwise abort calculating
	// the failover partition to avoid calculating on an inconsistent set of hosts.
	for host := range vmsByHost {
		if _, ok := hvsByName[host]; !ok {
			log.Error(nil, "vm is on unknown hypervisor, aborting", "host", host)
			return
		}
	}

	// For each source host S determine all target hosts toward which all vms
	// on S can be evacuated. These are our potential failover paths.
	failoverPaths := [][2]hv1.Hypervisor{}
	for sourceHost, vms := range vmsByHost {
		// All vms on the source host can be evacuated to here. targetHosts
		// is the intersection set of all candidate target hosts for the vms
		// on this source host.
		targetHosts := []hv1.Hypervisor{}
		for _, vm := range vms {
			candidates, err := c.fetchEvacuationCandidates(ctx, vm)
			if err != nil {
				log.Error(err, "failed to fetch evacuation candidates", "vm", vm.Name)
				continue
			}
			targetHosts = c.intersection(targetHosts, candidates)
		}
		for _, targetHv := range targetHosts {
			sourceHv := hvsByName[sourceHost]
			// We apply hard, gang-level constraints. We remove all results
			// where the target host is smaller than the source host. This
			// ensures that once we gang-schedule all vms during an evacuation,
			// that all vms fit the target host.
			if !c.targetLargeEnough(sourceHv, targetHv) {
				continue
			}
			failoverPaths = append(failoverPaths, [2]hv1.Hypervisor{sourceHv, targetHv})
		}
	}

	// Note: if the intersection set is empty because some of the vms on this
	// host have conflicting scheduling constraints, then this host has no
	// target host for now. In the future, we may tolerate this if single vms
	// don't fit into the same failover partition.

	// As a result of this process, we obtain a list of possible failover paths
	// between hosts. Now, we cluster this list of paths such that we obtain
	// failover partitions. For example, we may obtain a failover cluster for
	// HANA hosts, GP hosts, and external customer hosts. Algorithmically
	// speaking, we search for fully connected subgraphs inside the set of
	// edges. All nodes of a partition must be connected to all other nodes
	// inside the same partition.
	//
	// If an host is empty, it means that it doesn't have any outgoing edges.
	// These hosts will fall out as 1-element subgraphs as their own failover
	// partition from above algorithm. To recruit them into the correct
	// partition, we look for an existing failover partition in which all
	// nodes have outgoing edges to this host, and assume that the other
	// direction is also possible.
	partitions, err := c.clusterFailoverPaths(failoverPaths)
	if err != nil {
		log.Error(err, "failed to cluster failover paths")
		return
	}

	// For each of these failover partitions, we enforce a minimum ratio of
	// failover hosts for all other hosts inside this partition. This means, if
	// the partition consists of 10 hosts and the ratio is set to 0.1, this
	// results in 1 host inside this partition being converted to a failover
	// target.
	//
	// From the desired number of failover hosts in the partition, we first try
	// and find existing failover host reservations inside this partition. Each
	// existing reservation that is found is added to the desiredReservations
	// so that the host is stabilized and we don't flip between hosts. If there
	// are too many reservations for the desired number, we simply don't add
	// one of the existing reservations to the desiredReservations -- the
	// reconcile loop will prune them to free up space.
	//
	// If we can't fill the demand with existing reservations, we need to add
	// new ones to the desiredReservations map. For that we select the host
	// inside the failover partition with the lowest allocated capacity, to
	// reduce the overall resources that need to be migrated away.
	c.desiredReservationsMu.Lock()
	c.desiredReservations = make(map[corev1.ObjectReference]v1alpha1.Reservation)
	defer c.desiredReservationsMu.Unlock()
	for _, partition := range partitions {
		existingReservations := c.findExistingFailoverHostReservations(partition)
		hostsReserved := []string{}
		for _, reservation := range existingReservations {
			hostsReserved = append(hostsReserved, reservation.Status.Host)
		}
		availableReservations := existingReservations
		hvsNotReservedYet := []hv1.Hypervisor{}
		for _, hv := range partition {
			if !slices.Contains(hostsReserved, hv.Name) {
				hvsNotReservedYet = append(hvsNotReservedYet, hv)
			}
		}
		// TODO sort hvsNotReservedYet by allocated capacity
		for _, hv := range hvsNotReservedYet {
			az, ok := hv.Labels[corev1.LabelTopologyZone]
			if !ok {
				continue
			}
			sourceHosts := []string{} // other hosts in the same partition
			for _, otherHv := range partition {
				if otherHv.Name != hv.Name {
					sourceHosts = append(sourceHosts, otherHv.Name)
				}
			}
			availableReservations = append(availableReservations, v1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "failover-host-" + hv.Name,
				},
				Spec: v1alpha1.ReservationSpec{
					Type:                    v1alpha1.ReservationTypeFailoverHost,
					SchedulingDomain:        v1alpha1.SchedulingDomainNova,
					AvailabilityZone:        az,
					Resources:               hv.Status.Capacity,
					TargetHost:              hv.Name,
					FailoverHostReservation: &v1alpha1.FailoverHostReservationSpec{SourceHosts: sourceHosts},
				},
			})
		}

		// Limit to the number of reservations we actually need
		desiredCount := int(float64(len(partition)) * c.Config.Ratio)
		if desiredCount < c.Config.Minimum {
			desiredCount = c.Config.Minimum
		}
		if len(availableReservations) > desiredCount {
			availableReservations = availableReservations[:desiredCount]
		}

		for idx, reservation := range availableReservations {
			if idx >= desiredCount {
				break
			}
			c.desiredReservations[corev1.ObjectReference{Name: reservation.Name}] = reservation
		}
	}

	// Notify the reconcile loop that the desired reservations have changed
}
