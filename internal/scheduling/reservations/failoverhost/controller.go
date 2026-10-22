// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	novaapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/scheduling"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	vmv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

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

// Controller reconciles failover host reservations based on generated partitions.
// It runs a periodic partitioning algorithm out of which desired reservations
// are derived and used to update the actual failover host reservations in the
// cluster.
type Controller struct {
	client.Client // k8s client

	Config ControllerConfig

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

	// httpClient is used to send requests to the external scheduler service.
	httpClient *http.Client
}

// Reconcile reconciles a failover host reservation by comparing the desired state
// derived from the generated reservations with the actual state in the cluster,
// and making necessary updates to ensure consistency.
func (c *Controller) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = logf.FromContext(ctx).WithName("failoverhost-controller-reconcile")

	// TODO:
	// - Retrieve the reservation from the cluster using the request's namespaced name.
	// - Retrieve the reservation inside the desiredReservations map using the request's namespaced name.
	// - Compare the desired state (desiredReservations) map with the actual state from the cluster.
	// - Update the actual reservation in the cluster if it differs from the desired state.
	// - If the reservation is in the cluster but no longer in the desired state, it should be deleted.
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
// given virtual machine. It does this by creating an external scheduler
// request onto the cortex pipeline, putting in the scheduling constraints
// assigned to this specific virtual machine with evacuation intent.
func (c *Controller) fetchEvacuationCandidates(ctx context.Context, vm vmv1alpha1.VirtualMachine, hvsByName map[string]hv1.Hypervisor) ([]hv1.Hypervisor, error) {
	log := logf.FromContext(ctx)

	resMemoryMB, resVCPUs := uint64(0), uint64(0)
	if resMemory, ok := vm.Spec.Resources[vmv1alpha1.ResourceMemory]; ok {
		// Flavors are specified in Mebibytes, even if they declare "MB".
		resMemoryMB = uint64(resMemory.Value()) / 1024 / 1024
	}
	if resCPU, ok := vm.Spec.Resources[vmv1alpha1.ResourceCPU]; ok {
		resVCPUs = uint64(resCPU.Value())
	}
	imageProps := map[string]any{}
	for k, v := range vm.Spec.Nova.Image.Properties {
		imageProps[k] = v
	}
	requestObj := novaapi.ExternalSchedulerRequest{
		Options: scheduling.Options{
			ReadOnly: true, // Don't want to write any resources
			// We need to ignore existing failover host reservations because
			// this will otherwise block these hosts, separating them from
			// their failover partition and breaking the algorithm.
			IgnoredReservationTypes: []v1alpha1.ReservationType{
				v1alpha1.ReservationTypeFailoverHost,
			},
		},
		Spec: api.NovaObject[api.NovaSpec]{
			Data: api.NovaSpec{
				InstanceUUID:     vm.Name,
				NumInstances:     1,
				UserID:           vm.Spec.Nova.Ownership.User,
				ProjectID:        vm.Spec.Nova.Ownership.Project,
				AvailabilityZone: vm.Spec.AZ,
				// Avoid evacuating to the same host as the current VM.
				IgnoreHosts:    new([]string{vm.Status.Nova.Host}),
				SchedulerHints: map[string]any{"_nova_check_type": novaapi.EvacuateIntent},
				Flavor: api.NovaObject[api.NovaFlavor]{
					Data: api.NovaFlavor{
						Name:       vm.Spec.Nova.Flavor.Name,
						ExtraSpecs: vm.Spec.Nova.Flavor.ExtraSpecs,
						MemoryMB:   resMemoryMB,
						VCPUs:      resVCPUs,
					},
				},
				Image: api.NovaObject[api.NovaImageMeta]{
					Data: api.NovaImageMeta{
						ID: vm.Spec.Nova.Image.UUID,
						Properties: api.NovaObject[map[string]any]{
							Data: imageProps,
						},
					},
				},
			},
		},
	}
	log.V(1).Info("external scheduler request prepared", "request", requestObj)

	// Marshal the request
	reqBody, err := json.Marshal(requestObj)
	if err != nil {
		log.Error(err, "failed to marshal external scheduler request")
		return nil, fmt.Errorf("failed to marshal external scheduler request: %w", err)
	}

	// Create HTTP request with context
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.Config.ExternalSchedulerURL, bytes.NewReader(reqBody))
	if err != nil {
		log.Error(err, "failed to create HTTP request")
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Send the request and measure duration
	startTime := time.Now()
	response, err := c.httpClient.Do(httpReq)
	duration := time.Since(startTime)
	if err != nil {
		log.Error(err, "failed to send external scheduler request")
		return nil, fmt.Errorf("failed to send external scheduler request: %w", err)
	}
	defer response.Body.Close()
	log.V(1).Info("external scheduler request sent",
		"duration", duration, "status", response.StatusCode)

	if response.StatusCode != http.StatusOK {
		log.Error(nil, "external scheduler request failed", "status", response.StatusCode)
		return nil, fmt.Errorf("external scheduler request failed with status: %d", response.StatusCode)
	}

	var responseObj api.ExternalSchedulerResponse
	if err := json.NewDecoder(response.Body).Decode(&responseObj); err != nil {
		log.Error(err, "failed to decode external scheduler response")
		return nil, fmt.Errorf("failed to decode external scheduler response: %w", err)
	}

	log.V(1).Info("external scheduler response received", "response", responseObj)
	evacuationCandidates := []hv1.Hypervisor{}
	for _, hv := range hvsByName {
		if slices.Contains(responseObj.Hosts, hv.Name) {
			evacuationCandidates = append(evacuationCandidates, hv)
		}
	}
	return evacuationCandidates, nil
}

// clusterFailoverPaths clusters the given failover paths into fully connected
// subgraphs, representing failover partitions. Each partition contains a set of
// hypervisors that are mutually reachable via failover paths.
func (c *Controller) clusterFailoverPaths(paths [][2]hv1.Hypervisor) ([][]hv1.Hypervisor, error) {
	return nil, nil // TODO
}

// cmpResources compares the resources of hypervisor a with the resources
// of hypervisor b. If the resources of a are greater then b, it returns 1.
// If the resources of a are less than b, it returns -1.
// If the resources are equal, it returns 0.
func cmpResources(a, b map[hv1.ResourceName]resource.Quantity) int {
	// TODO: Implement the logic to compare the resources of hypervisors a and b.
	return 0
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
		hv, ok := hvsByName[host]
		if !ok {
			log.Error(nil, "vm is on unknown hypervisor, aborting", "host", host)
			return
		}
		// Also ensure that the hypervisor has an availability zone assigned.
		if _, ok := hv.Labels[corev1.LabelTopologyZone]; !ok {
			log.Error(nil, "hypervisor has no availability zone, aborting", "host", host)
			return
		}
	}
	// Find existing failover host reservations
	allReservations := &v1alpha1.ReservationList{}
	if err := c.List(ctx, allReservations); err != nil {
		log.Error(err, "failed to list existing failover host reservations")
		return
	}
	existingFailoverHostReservations := make(map[corev1.ObjectReference]v1alpha1.Reservation)
	for _, reservation := range allReservations.Items {
		if reservation.Spec.Type == v1alpha1.ReservationTypeFailoverHost {
			existingFailoverHostReservations[corev1.ObjectReference{Name: reservation.Name}] = reservation
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
		for vmidx, vm := range vms {
			vmTargetHosts, err := c.fetchEvacuationCandidates(ctx, vm, hvsByName)
			if err != nil {
				log.Error(err, "failed to fetch evacuation candidates", "vm", vm.Name)
				continue
			}
			if vmidx == 0 {
				targetHosts = vmTargetHosts // Start with all candidates of the first vm
				continue
			}
			if len(targetHosts) == 0 {
				break // No need to intersect further, will be empty
			}
			intersection := []hv1.Hypervisor{}
			for _, vmTargetHost := range vmTargetHosts {
				for _, targetHost := range targetHosts {
					if vmTargetHost.Name == targetHost.Name {
						intersection = append(intersection, targetHost)
					}
				}
			}
			targetHosts = intersection
		}
		for _, targetHv := range targetHosts {
			sourceHv := hvsByName[sourceHost]
			// We apply hard, gang-level constraints. We remove all results
			// where the target host is smaller than the source host. This
			// ensures that once we gang-schedule all vms during an evacuation,
			// that all vms fit the target host.
			sourceHvCapa := sourceHv.Status.Capacity // fallback to base capacity
			for resName, resQty := range sourceHv.Status.EffectiveCapacity {
				sourceHvCapa[resName] = resQty
			}
			targetHvCapa := targetHv.Status.Capacity
			for resName, resQty := range targetHv.Status.EffectiveCapacity {
				targetHvCapa[resName] = resQty
			}
			if cmpResources(sourceHvCapa, targetHvCapa) > 0 {
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
		// Find the reservations that are part of this partition.
		// The available reservations slice will be constructed as follows:
		// [res_existing_1, res_existing_2, ..., res_new_1, res_new_2, ...]
		// Then we can go from index 0 to desiredCount to pick the reservations
		// we actually need.
		availableReservations := []v1alpha1.Reservation{}
		hostsReserved := []string{}
		for _, reservation := range existingFailoverHostReservations {
			hostsReserved = append(hostsReserved, reservation.Status.Host)
			for _, hv := range partition {
				if reservation.Spec.TargetHost == hv.Name {
					availableReservations = append(availableReservations, reservation)
					break
				}
			}
		}
		// Sort [res_new_1, res_new_2, ...] by allocated capacity (ascending)
		// so that creating this failover host costs as few migrated resources
		// as possible.
		sort.SliceStable(availableReservations, func(i, j int) bool {
			return cmpResources(availableReservations[i].Spec.Resources, availableReservations[j].Spec.Resources) < 0
		})
		for _, hv := range partition {
			if slices.Contains(hostsReserved, hv.Name) {
				continue // Don't create a reservation for a host that is already reserved
			}
			sourceHosts := []string{} // Other hosts in the same partition
			for _, otherHv := range partition {
				if otherHv.Name != hv.Name {
					sourceHosts = append(sourceHosts, otherHv.Name)
				}
			}
			reservedResources := hv.Status.Capacity // Fallback to base capacity
			for resName, resQty := range hv.Status.EffectiveCapacity {
				reservedResources[resName] = resQty
			}
			availableReservations = append(availableReservations, v1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "failover-host-" + hv.Name,
					Labels: map[string]string{
						v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelFailoverHost,
					},
				},
				Spec: v1alpha1.ReservationSpec{
					Type:                    v1alpha1.ReservationTypeFailoverHost,
					SchedulingDomain:        v1alpha1.SchedulingDomainNova,
					AvailabilityZone:        hv.Labels[corev1.LabelTopologyZone],
					Resources:               reservedResources, // Allocate full host
					TargetHost:              hv.Name,
					FailoverHostReservation: &v1alpha1.FailoverHostReservationSpec{SourceHosts: sourceHosts},
				},
			})
		}
		// We now have the available reservations complete and in the correct order.
		// Next, we determine the desired count of failover host reservations
		// based on the configured ratio and minimum, and pick the top available
		// reservations accordingly.
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
	reservationsToReconcile := make(map[corev1.ObjectReference]v1alpha1.Reservation)
	for k, v := range existingFailoverHostReservations {
		reservationsToReconcile[k] = v
	}
	for k, v := range c.desiredReservations {
		reservationsToReconcile[k] = v
	}
	for _, reservation := range reservationsToReconcile {
		evt := event.GenericEvent{Object: &reservation}
		select {
		case c.reconcileCh <- evt:
		case <-ctx.Done():
			return
		}
	}
}

// SetupWithManager wires the periodic loop into the manager and registers
// the reconciler to consume failover host reservation events it enqueues.
func (c *Controller) SetupWithManager(mgr ctrl.Manager) error {
	if mgr == nil {
		return fmt.Errorf("manager cannot be nil")
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
