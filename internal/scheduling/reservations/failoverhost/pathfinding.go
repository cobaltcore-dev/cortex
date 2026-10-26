// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"time"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/scheduling"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	vmv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// PathFinder is the first pipeline stage. It discovers the directed edges of the
// failover graph: for every occupied host it reports the hosts that host's
// entire workload could be gang-evacuated onto. An edge source->target means
// "all vms on source can be evacuated onto target, and target is large enough to
// hold them all at once".
//
// The direction matters. source->target does not imply target->source: target
// might be too small for source's workload even if the reverse fits. The
// PartitionBuilder later keeps only the mutual edges, so this stage is free to
// report one-directional possibilities.
type PathFinder interface {
	// FindPaths returns the failover edges as [source, target] hypervisor pairs.
	// A vm that the scheduler has no target for yields an empty candidate set and
	// is not an error; it just can't contribute a path. Any lookup error, by
	// contrast, is an infrastructure failure (scheduler unreachable/timed out/
	// non-200) that invalidates the pass, so it is returned to abort it rather
	// than silently collapsing the whole failover graph.
	FindPaths(ctx context.Context, topo topology) ([][2]hv1.Hypervisor, error)
}

// evacuationPathFinder finds failover paths by asking the external cortex
// scheduler, per vm, where that vm could be evacuated to, then intersecting the
// answers across all vms on a host and filtering by gang-level fit.
//
// Asking the real scheduler (rather than reimplementing placement here) is
// deliberate: it means failover paths are computed with exactly the same
// filters, weighers and constraints that govern ordinary placement, so a
// partition can never contain a target the scheduler would actually reject.
type evacuationPathFinder struct {
	// httpClient issues the scheduler requests. Give it a timeout so a hung
	// scheduler cannot stall the whole calculation loop. Separately injected so
	// tests can point it at a stub server.
	httpClient *http.Client
	// schedulerURL is the external cortex scheduler endpoint to POST to.
	schedulerURL string
	// monitor records scheduler request metrics. May be nil (e.g. in tests).
	monitor *Monitor
}

// FindPaths implements PathFinder.
//
// For a source host S, a target host T is a valid failover target only if every
// vm on S can be evacuated onto T. We therefore query the scheduler per vm and
// keep the running intersection of the candidate sets: a target survives only if
// it appears for every vm on the host. (If the intersection comes out empty —
// e.g. because vms on S carry conflicting scheduling constraints — then S has no
// failover target for now. In the future we may relax this to tolerate single
// vms that don't fit the same partition as the rest.)
//
// Finally we apply the hard, gang-level constraint: an evacuation moves all of
// S's vms onto T at once, so T must be at least as large as S. We drop every
// target smaller than its source before emitting the edge. Capacity is compared
// on a fresh copy of the hypervisor's capacity maps so we never mutate the
// shared Status.Capacity.
func (f *evacuationPathFinder) FindPaths(ctx context.Context, topo topology) ([][2]hv1.Hypervisor, error) {
	log := logf.FromContext(ctx)

	paths := [][2]hv1.Hypervisor{}
	for sourceHost, vms := range topo.vmsByHost {
		// targetHosts is the running intersection of candidate targets across
		// all vms on this source host. initialized tracks whether we've seeded it
		// with the first vm's candidates yet.
		targetHosts := []hv1.Hypervisor{}
		initialized := false
		for _, vm := range vms {
			vmTargetHosts, err := f.fetchEvacuationCandidates(ctx, vm, topo.hypervisors)
			if err != nil {
				// A lookup error is an infrastructure failure (the scheduler is
				// unreachable, timed out, or returned a non-200) — not an answer
				// that this vm has no candidates, which comes back as an empty
				// slice with a nil error. We cannot assert anything about the
				// failover graph on a partial view, so abort the whole pass. The
				// caller leaves the desired state untouched and retries next
				// cycle; swallowing the error here would instead collapse every
				// host's paths and prune all still-valid reservations.
				return nil, fmt.Errorf(
					"failed to fetch evacuation candidates for vm %q on host %q: %w",
					vm.Name, sourceHost, err)
			}
			// Log out the target hosts for this vm.
			vmTargetHostNames := make([]string, len(vmTargetHosts))
			for i, host := range vmTargetHosts {
				vmTargetHostNames[i] = host.Name
			}
			log.V(1).Info("fetched evacuation candidates for vm",
				"sourceHost", sourceHost, "vm", vm.Name, "targetHosts", vmTargetHostNames)
			if !initialized {
				targetHosts = vmTargetHosts // Seed with the first vm's candidates.
				initialized = true
				continue
			}
			if len(targetHosts) == 0 {
				break // Intersection already empty, nothing can survive.
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
		// Note: if the intersection is empty because some vms on this host have
		// conflicting scheduling constraints, then this host has no target host
		// for now. In the future, we may tolerate this if single vms don't fit
		// into the same failover partition.
		for _, targetHv := range targetHosts {
			sourceHv := topo.hypervisors[sourceHost]
			// Apply the hard, gang-level constraint: drop results where the
			// target host is smaller than the source host, so that once we
			// gang-schedule all vms during an evacuation they all fit the
			// target. We copy into fresh maps so we don't mutate the shared
			// hypervisor Status.Capacity.
			sourceHvCapa := map[hv1.ResourceName]resource.Quantity{}
			maps.Copy(sourceHvCapa, sourceHv.Status.Capacity) // base capacity
			maps.Copy(sourceHvCapa, sourceHv.Status.EffectiveCapacity)
			targetHvCapa := map[hv1.ResourceName]resource.Quantity{}
			maps.Copy(targetHvCapa, targetHv.Status.Capacity)
			maps.Copy(targetHvCapa, targetHv.Status.EffectiveCapacity)
			if cmpResources(sourceHvCapa, targetHvCapa) > 0 {
				log.V(1).Info("dropping failover path, target smaller than source",
					"sourceHost", sourceHv.Name, "targetHost", targetHv.Name)
				continue
			}
			paths = append(paths, [2]hv1.Hypervisor{sourceHv, targetHv})
			log.V(1).Info("added failover path",
				"sourceHost", sourceHv.Name, "targetHost", targetHv.Name)
		}
	}
	return paths, nil
}

// fetchEvacuationCandidates asks the external cortex scheduler where a single vm
// could be evacuated to, and returns the subset of known hypervisors it accepts.
//
// The request is shaped to mirror a real evacuation as closely as possible: it
// carries the vm's owner, availability zone, flavor and image so the same
// filters apply, offers every known host as a candidate (Hosts) so the scheduler
// has a host set to filter over, sets the evacuate scheduler hint, and ignores
// the vm's current host as an evacuation target. Four options matter:
//
//   - ReadOnly, so probing placements never mutate shared scheduling state.
//   - SkipInflight and SkipCommittedResourceTracking, which ReadOnly runs must
//     set (scheduling.Options.Validate rejects a ReadOnly run otherwise, and the
//     scheduler calls Validate) so this probe neither creates in-flight
//     reservations nor writes CR allocations.
//   - IgnoredReservationTypes = FailoverHost, so existing failover host
//     reservations don't block the very hosts we are reasoning about — otherwise
//     a reserved host would be hidden from its own partition and the clustering
//     would fragment.
//   - AssumeEmptyHosts, so candidates are judged by their total capacity rather
//     than their current free capacity. A failover target is reserved *empty*, so
//     what matters for partitioning is whether a host could hold the workload once
//     cleared, not whether it has room right now. Without this a busy host never
//     accepts anyone, gets no inbound links, forms no mutual edge, and stays
//     orphaned — even though reserving it would drain it. Compatibility
//     (flavors, traits, AZ) is thus occupancy-independent; which host is actually
//     cheapest to empty is left to the planner's candidate ordering. The hard
//     whole-host fit check in FindPaths still compares *total* capacities, so an
//     undersized target is dropped regardless of this option.
func (f *evacuationPathFinder) fetchEvacuationCandidates(ctx context.Context, vm vmv1alpha1.VirtualMachine, hvsByName map[string]hv1.Hypervisor) ([]hv1.Hypervisor, error) {
	log := logf.FromContext(ctx)

	resMemoryMB, resVCPUs := uint64(0), uint64(0)
	if resMemory, ok := vm.Spec.Resources[vmv1alpha1.ResourceMemory]; ok {
		// Flavors are specified in Mebibytes, even if they declare "MB".
		if v := resMemory.Value(); v > 0 {
			resMemoryMB = uint64(v) / 1024 / 1024
		}
	}
	if resCPU, ok := vm.Spec.Resources[vmv1alpha1.ResourceCPU]; ok {
		if v := resCPU.Value(); v > 0 {
			resVCPUs = uint64(v)
		}
	}
	imageProps := map[string]any{}
	for k, v := range vm.Spec.Nova.Image.Properties {
		imageProps[k] = v
	}
	// Offer every known host as a candidate. The scheduler filters/weighs over
	// exactly this host set — it does not gather the cluster itself — so leaving
	// Hosts empty would make it return no candidates. Each host also needs a
	// weight entry, which the scheduler requires to accompany the host set.
	hosts := make([]api.ExternalSchedulerHost, 0, len(hvsByName))
	weights := make(map[string]float64, len(hvsByName))
	for name := range hvsByName {
		hosts = append(hosts, api.ExternalSchedulerHost{ComputeHost: name})
		weights[name] = 0
	}
	requestObj := api.ExternalSchedulerRequest{
		Hosts:   hosts,
		Weights: weights,
		Options: scheduling.Options{
			// Read-only probe: don't write any resources. ReadOnly additionally
			// requires SkipInflight and SkipCommittedResourceTracking, else the
			// scheduler rejects the request in Options.Validate.
			ReadOnly:                      true,
			SkipInflight:                  true,
			SkipCommittedResourceTracking: true,
			// Judge candidates by total capacity, not current free capacity: a
			// failover target is reserved empty, so a busy host that would fit the
			// workload once cleared must still be a valid candidate. Without this,
			// occupied hosts never accept anyone and stay orphaned.
			AssumeEmptyHosts: true,
			// Ignore existing failover host reservations, otherwise they would
			// block these hosts, separate them from their failover partition and
			// break the clustering.
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
				SchedulerHints: map[string]any{"_nova_check_type": api.EvacuateIntent},
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

	reqBody, err := json.Marshal(requestObj)
	if err != nil {
		log.Error(err, "failed to marshal external scheduler request")
		return nil, fmt.Errorf("failed to marshal external scheduler request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		f.schedulerURL, bytes.NewReader(reqBody))
	if err != nil {
		log.Error(err, "failed to create HTTP request")
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	startTime := time.Now()
	response, err := f.httpClient.Do(httpReq)
	duration := time.Since(startTime)
	if err != nil {
		f.monitor.RecordSchedulerRequest("error", duration)
		log.Error(err, "failed to send external scheduler request")
		return nil, fmt.Errorf("failed to send external scheduler request: %w", err)
	}
	defer response.Body.Close()
	f.monitor.RecordSchedulerRequest(strconv.Itoa(response.StatusCode), duration)
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
