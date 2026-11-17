// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

// Package compute is the in-process entrypoint for Nova placement scheduling.
//
// It defines a well-typed, Nova-agnostic PlacementRequest that the
// filter-weigher pipeline consumes, and a Placement that owns the single shared
// scheduling lock and runs the pipeline plus caller-provided side-effect
// closures. Callers that run in the same process (the Nova HTTP adapter, the
// reservation/commitment/capacity/failover controllers) call the Placement
// directly instead of POSTing to the external HTTP endpoint.
//
// The spec deliberately contains NO loosely-typed maps or Nova string
// conventions (flavor extra-spec keys, scheduler-hint keys, image property
// keys). A Nova spec adapter is responsible for translating Nova's over-the-wire
// shape into these typed fields; the pipeline only ever sees well-specced
// values. The Nova vocabulary types those fields use (capabilities, flavor
// sizing, hypervisor family, …) live in the api/external/nova package.
package compute

import (
	"strings"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/scheduling"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// Host is a placement candidate. For KVM hosts the compute host name and the
// hypervisor hostname are identical; for others they may differ.
type Host struct {
	// ComputeHost is the Nova compute host name, e.g. nova-compute-bb123.
	ComputeHost string
	// HypervisorHostname is the hypervisor hostname, e.g. domain-c123.<uuid>.
	HypervisorHostname string
}

// PlacementRequest is the well-typed, Nova-agnostic input to the scheduling
// pipeline. Every field is a resolved scalar, enum, or typed collection — there
// are no maps of Nova-specific string keys and no values the pipeline must parse.
//
// Each field documents what leaving it at its zero value means, so the Nova spec
// adapter (and any other caller) can see exactly which fields are load-bearing.
//
// PlacementRequest implements lib.FilterWeigherPipelineRequest so it can be the
// RequestType of the filter-weigher pipeline.
type PlacementRequest struct {
	// Pipeline is the name of the pipeline to execute. Required: an empty name
	// resolves no pipeline and the run fails with "pipeline not found".
	Pipeline string
	// GatherAllCandidates, when true, tells the Placement to ignore the Hosts and
	// Weights the caller provided and instead gather ALL available placement
	// candidates (via the CandidateGatherer) before filtering. It is set for
	// requests where cortex can enumerate the candidate hosts itself — today the
	// KVM family, whose hypervisors cortex knows from the hypervisor operator;
	// VMware placement leaves it false and relies on the caller's preselection.
	// When true, Hosts/Weights below are overwritten before filtering.
	GatherAllCandidates bool
	// Hosts are the placement candidates. An empty list yields an empty result
	// (no host found), unless GatherAllCandidates is set, in which case Hosts is
	// overwritten before filtering.
	Hosts []Host
	// Weights are the per-host input weights, keyed by compute host name. A host
	// absent from this map is weighed as 0. Empty means every candidate starts
	// equally weighted.
	Weights map[string]float64
	// Options configure the pipeline behavior for this run. The zero value is a
	// normal (non-read-only) run with no candidate limit; see scheduling.Options
	// for per-field defaults.
	Options scheduling.Options

	// Intent is the resolved scheduling intent (replaces the "_nova_check_type"
	// scheduler hint). The zero value is the empty intent; intent-gated
	// filters/weighers treat it like an initial placement (create) and a forced
	// destination is honored (see IsForcedDestination).
	Intent v1alpha1.SchedulingIntent

	// InstanceUUID identifies the instance being placed. Empty is allowed for
	// synthetic runs (e.g. capacity probes); it only appears in logs and side
	// effects, so an empty value disables nothing in the pipeline itself.
	InstanceUUID string
	// ProjectID is the owning OpenStack project. Empty disables project-scoped
	// filtering (e.g. allowed-projects, quota) — those steps treat it as "no
	// project context".
	ProjectID string
	// UserID is the requesting user. Empty only affects trace logging and the VM
	// ownership written by side effects.
	UserID string
	// AvailabilityZone is the target AZ. Empty means no AZ constraint, so the
	// correct-AZ filter keeps all hosts.
	AvailabilityZone string
	// NumInstances is the number of instances this request represents. It is a
	// MULTIPLIER in capacity and bin-pack math, so 0 makes a host look infinitely
	// free and must not be left unset — the adapter always sets it (Nova sends
	// >= 1).
	NumInstances uint64

	// IgnoreHosts are hosts to exclude from placement. Empty/nil means no hosts
	// are excluded.
	IgnoreHosts []string
	// ForceHosts pins placement to these compute hosts. Empty/nil means no host
	// is forced. When set (and the intent does not require filter validation),
	// the pipeline is skipped and only forced hosts are returned.
	ForceHosts []string
	// ForceNodes pins placement to these hypervisor hostnames. Empty/nil means no
	// node is forced. Semantics mirror ForceHosts.
	ForceNodes []string

	// HypervisorFamily is the coarse family (kvm/vmware) used by the pipeline and
	// routing. Empty means unresolved: KVM-only gates (e.g. VM creation) treat it
	// as not-KVM and skip.
	HypervisorFamily api.HypervisorFamily
	// ImageHypervisorType is the hypervisor type declared by the image. The empty
	// value (ImageHypervisorTypeUnset) means the image declared none ("no image
	// constraint"); ImageHypervisorTypeUnparseable means it declared one that
	// could not be parsed, which the image-properties filter surfaces as a
	// diagnostic.
	ImageHypervisorType api.ImageHypervisorType
	// HardwareVersion is resolved from the "quota:hw_version" extra spec. Empty
	// means no hardware-version constraint.
	HardwareVersion string

	// InstanceCapacity is the per-instance capacity the request asks for, keyed
	// by hv1 resource name: hv1.ResourceCPU is the vCPU count (DecimalSI) and
	// hv1.ResourceMemory is memory in bytes (BinarySI). This mirrors the shape the
	// hypervisor operator advertises (hv1.Hypervisor.Status.Capacity) and that
	// reservation slots use, so capacity/bin-pack steps consume it directly. A nil
	// map (or a missing/zero entry) means a zero-sized instance to those steps
	// (fits anywhere) — the adapter always populates it for real placements. Use
	// TotalCapacityDemand for the across-all-instances total.
	InstanceCapacity map[hv1.ResourceName]resource.Quantity

	// RequiredTraits are trait names the host must have (adapter stripped the
	// "trait:" prefix and kept the "required" ones). Empty/nil means no required
	// traits, so the traits filter imposes no inclusion constraint.
	RequiredTraits []string
	// ForbiddenTraits are trait names the host must NOT have. Empty/nil means no
	// forbidden traits. If both trait lists are empty the traits filter is a no-op.
	ForbiddenTraits []string
	// RequestedCapabilities are required (key=value) hypervisor capabilities
	// (adapter stripped the "capabilities:" prefix and rejected unsupported
	// operators). Empty/nil means the capabilities filter is a no-op.
	RequestedCapabilities []api.Capability
	// AcceleratorsRequested reports whether the flavor requests a hardware
	// accelerator (adapter resolves it from the presence of the
	// "accel:device_profile" extra spec). When true, the accelerators filter keeps
	// only hosts carrying the COMPUTE_ACCELERATORS trait; false is a no-op.
	AcceleratorsRequested bool

	// SourceHost is the migration source (was hint "source_host"). Empty means
	// not a host-to-host move, so the live-migratable filter imposes no source
	// constraint.
	SourceHost string
	// DomainName is the customer domain (was hint "domain_name"). Empty means no
	// domain context, so the external-customer filter is skipped.
	DomainName string
	// ResourceGroup is the failover resource group (was hint
	// "_cortex_resource_group"). Empty means no group, so group-consolidation
	// weighing is skipped.
	ResourceGroup string

	// RequestedDestination is the requested target host/aggregate. Nil means no
	// destination constraint (see the type doc).
	RequestedDestination *api.RequestedDestination
	// InstanceGroup is the server group the instance belongs to. Nil means the
	// instance is not grouped, so affinity/anti-affinity steps are skipped (see
	// the type doc).
	InstanceGroup *api.InstanceGroupSpec

	// RequestID identifies this scheduling request for tracing. Empty only
	// affects log correlation.
	RequestID string
	// GlobalRequestID is the cross-service request ID for tracing. Empty only
	// affects log correlation.
	GlobalRequestID string
}

// Compile-time check that PlacementRequest satisfies the pipeline request contract.
var _ lib.FilterWeigherPipelineRequest = PlacementRequest{}

// GetIntent returns the resolved scheduling intent.
func (r PlacementRequest) GetIntent() v1alpha1.SchedulingIntent { return r.Intent }

// GetHosts returns the compute host names of all candidates.
func (r PlacementRequest) GetHosts() []string {
	hosts := make([]string, len(r.Hosts))
	for i, h := range r.Hosts {
		hosts[i] = h.ComputeHost
	}
	return hosts
}

// GetWeights returns the per-host input weights.
func (r PlacementRequest) GetWeights() map[string]float64 { return r.Weights }

// TotalCapacityDemand returns InstanceCapacity scaled by NumInstances, i.e. the total
// capacity across all instances this request represents. Each quantity keeps its
// source format (DecimalSI for cpu, BinarySI for memory). NumInstances is the
// single source of truth for the multiplier; the adapter always sets it >= 1. A
// nil InstanceCapacity yields an empty map.
func (r PlacementRequest) TotalCapacityDemand() map[hv1.ResourceName]resource.Quantity {
	total := make(map[hv1.ResourceName]resource.Quantity, len(r.InstanceCapacity))
	factor := int64(r.NumInstances) //nolint:gosec // instance count is bounded by Nova
	for name, q := range r.InstanceCapacity {
		scaled := q.DeepCopy()
		scaled.Mul(factor)
		total[name] = scaled
	}
	return total
}

// GetOptions returns the call-time options for this run.
func (r PlacementRequest) GetOptions() scheduling.Options { return r.Options }

// GetTraceLogArgs returns alternating key/value pairs for the step trace log.
func (r PlacementRequest) GetTraceLogArgs() []any {
	return []any{
		"greq", r.GlobalRequestID,
		"req", r.RequestID,
		"user", r.UserID,
		"project", r.ProjectID,
	}
}

// Filter returns a copy of the request narrowed to the given hosts, with the
// weights updated to the provided values. Hosts not present in includedHosts are
// dropped and won't be reconsidered by later steps.
func (r PlacementRequest) Filter(includedHosts map[string]float64) lib.FilterWeigherPipelineRequest {
	filteredHosts := make([]Host, 0, len(includedHosts))
	for _, h := range r.Hosts {
		if _, ok := includedHosts[h.ComputeHost]; ok {
			filteredHosts = append(filteredHosts, h)
		}
	}
	r.Hosts = filteredHosts
	filteredWeights := make(map[string]float64, len(includedHosts))
	for host, weight := range includedHosts {
		filteredWeights[host] = weight
	}
	r.Weights = filteredWeights
	return r
}

// IsForcedDestination reports whether the request is forced onto specific
// hosts/nodes and the scheduler filters should be skipped entirely. A forced
// request that still carries a non-create intent (rebuild/evacuate/resize) must
// be validated by the filters, so it is not treated as forced here. Intent
// resolution happens in the adapter; this only inspects the typed Intent field.
func (r PlacementRequest) IsForcedDestination() bool {
	if len(r.ForceHosts) == 0 && len(r.ForceNodes) == 0 {
		return false
	}
	return r.Intent == "" || r.Intent == v1alpha1.SchedulingIntentUnknown
}

// ForcedHosts returns the subset of request hosts matching the forced
// destinations. force_hosts matches on compute host name (case-insensitive);
// force_nodes matches on hypervisor hostname (case-sensitive); when both are set
// a host must match both. Order is preserved and compute hosts are deduplicated.
func (r PlacementRequest) ForcedHosts() []string {
	forceHostSet := make(map[string]bool, len(r.ForceHosts))
	for _, h := range r.ForceHosts {
		forceHostSet[strings.ToLower(h)] = true
	}
	forceNodeSet := make(map[string]bool, len(r.ForceNodes))
	for _, n := range r.ForceNodes {
		forceNodeSet[n] = true
	}
	matched := make([]string, 0, len(r.Hosts))
	seen := make(map[string]bool, len(r.Hosts))
	for _, h := range r.Hosts {
		if len(forceHostSet) > 0 && !forceHostSet[strings.ToLower(h.ComputeHost)] {
			continue
		}
		if len(forceNodeSet) > 0 && !forceNodeSet[h.HypervisorHostname] {
			continue
		}
		key := strings.ToLower(h.ComputeHost)
		if seen[key] {
			continue
		}
		seen[key] = true
		matched = append(matched, h.ComputeHost)
	}
	return matched
}
