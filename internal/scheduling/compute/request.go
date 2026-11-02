// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

// Package compute is the in-process entrypoint for Nova placement scheduling.
//
// It defines a well-typed, Nova-agnostic SchedulerRequest that the
// filter-weigher pipeline consumes, and a Scheduler that owns the single shared
// scheduling lock and runs the pipeline plus caller-provided side-effect
// closures. Callers that run in the same process (the Nova HTTP adapter, the
// reservation/commitment/capacity/failover controllers) call the Scheduler
// directly instead of POSTing to the external HTTP endpoint.
//
// The spec deliberately contains NO loosely-typed maps or Nova string
// conventions (flavor extra-spec keys, scheduler-hint keys, image property
// keys). A Nova spec adapter is responsible for translating Nova's over-the-wire
// shape into these typed fields; the pipeline only ever sees well-specced
// values.
package compute

import (
	"strings"

	"github.com/cobaltcore-dev/cortex/api/scheduling"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"k8s.io/apimachinery/pkg/api/resource"
)

// HypervisorFamily is the coarse hypervisor family a request targets. It is the
// distinction the pipeline and request routing care about; the more specific
// HypervisorType (QEMU vs CH within the KVM family) is only needed by Nova side
// effects. The adapter resolves the family from Nova's
// "capabilities:hypervisor_type" flavor extra spec.
type HypervisorFamily string

const (
	// HypervisorFamilyKVM covers KVM-based hypervisors (QEMU and CH).
	HypervisorFamilyKVM HypervisorFamily = "kvm"
	// HypervisorFamilyVMware covers VMware hypervisors.
	HypervisorFamilyVMware HypervisorFamily = "vmware"
)

// HypervisorType is the specific hypervisor type of a request. The adapter
// resolves it from Nova's "capabilities:hypervisor_type" flavor extra spec.
type HypervisorType string

const (
	// HypervisorTypeQEMU represents QEMU/KVM hypervisors.
	HypervisorTypeQEMU HypervisorType = "qemu"
	// HypervisorTypeCH represents Cloud-Hypervisor/KVM hypervisors.
	HypervisorTypeCH HypervisorType = "ch"
	// HypervisorTypeVMware represents VMware hypervisors.
	HypervisorTypeVMware HypervisorType = "vmware"
)

// FlavorType classifies the workload type. The adapter resolves it from Nova's
// "trait:CUSTOM_HANA_EXCLUSIVE_HOST" flavor extra spec.
type FlavorType string

const (
	// FlavorTypeGeneralPurpose represents general purpose workloads.
	FlavorTypeGeneralPurpose FlavorType = "general-purpose"
	// FlavorTypeHANA represents HANA workloads.
	FlavorTypeHANA FlavorType = "hana"
)

// ImageHypervisorType is the hypervisor type declared by the image. The adapter
// resolves it from Nova's image properties ("img_hv_type"/"hypervisor_type").
type ImageHypervisorType string

const (
	// ImageHypervisorTypeVMware indicates a VMware image.
	ImageHypervisorTypeVMware ImageHypervisorType = "vmware"
	// ImageHypervisorTypeBaremetal indicates a baremetal image.
	ImageHypervisorTypeBaremetal ImageHypervisorType = "baremetal"
	// ImageHypervisorTypeKVM indicates a KVM image.
	ImageHypervisorTypeKVM ImageHypervisorType = "kvm"
	// ImageHypervisorTypeUnset indicates no image hypervisor type was declared.
	ImageHypervisorTypeUnset ImageHypervisorType = ""
	// ImageHypervisorTypeUnparseable indicates an image hypervisor type was declared but could not be parsed.
	ImageHypervisorTypeUnparseable ImageHypervisorType = "unparseable"
)

// Host is a placement candidate. For KVM hosts the compute host name and the
// hypervisor hostname are identical; for others they may differ.
type Host struct {
	// ComputeHost is the Nova compute host name, e.g. nova-compute-bb123.
	ComputeHost string
	// HypervisorHostname is the hypervisor hostname, e.g. domain-c123.<uuid>.
	HypervisorHostname string
}

// Capability is a single required hypervisor capability as a typed key/value
// equality, e.g. {Name: "cpu_arch", Value: "x86_64"}. The adapter resolves these
// from Nova's "capabilities:<key>" flavor extra specs and rejects any entry that
// uses an unsupported comparison operator before it reaches the pipeline.
type Capability struct {
	// Name is the capability key without the "capabilities:" prefix.
	Name string
	// Value is the required exact value.
	Value string
}

// FlavorSpec is the typed, Nova-agnostic description of the requested flavor.
// It carries no extra-spec map: the adapter resolves every extra spec the
// pipeline needs into the typed fields on this struct and on SchedulerRequest.
type FlavorSpec struct {
	// Name is the flavor name. Empty only affects CR/flavor-group lookups in
	// side effects, not the pipeline math.
	Name string
	// FlavorID is the flavor ID. Empty only affects the VM resource written by
	// side effects.
	FlavorID string
	// Memory is the requested memory per instance, as a resource.Quantity in
	// bytes. The zero value is a zero-memory instance to capacity/bin-pack steps
	// (fits anywhere), so the adapter sets it (from Nova's memory_mb * 2^20).
	Memory resource.Quantity
	// VCPUs is the requested vCPUs per instance. 0 means a zero-CPU instance to
	// capacity/bin-pack steps (fits anywhere), so the adapter sets it.
	VCPUs uint64
	// Type classifies the workload (general-purpose/hana), resolved by the
	// adapter from the flavor's extra specs. Empty is treated as general-purpose
	// by pipeline inference.
	Type FlavorType
}

// RequestedDestination mirrors the subset of Nova's requested_destination that
// the pipeline reads. A nil *RequestedDestination on the request means no
// destination constraint was requested.
type RequestedDestination struct {
	// Host is the requested target host. Empty means no host was pinned (only an
	// aggregate constraint applies).
	Host string
	// Aggregates are the host-aggregate names the destination must belong to.
	// Empty/nil means no aggregate constraint.
	Aggregates []string
}

// InstanceGroupSpec is the typed subset of a Nova server group the pipeline
// reads. A nil *InstanceGroupSpec on the request means the instance is not part
// of a server group, so group affinity/anti-affinity filters are skipped.
type InstanceGroupSpec struct {
	// UUID identifies the server group. Empty if unknown.
	UUID string
	// Policy is the group policy (e.g. "anti-affinity", "soft-affinity"). Empty
	// means no policy-specific filter/weigher applies.
	Policy string
	// Hosts are the hosts already occupied by members of this group. Empty/nil
	// means the group currently occupies no hosts.
	Hosts []string
	// Members are the instance UUIDs already in the group. Empty/nil means the
	// group has no members yet, so anti-affinity has nothing to exclude.
	Members []string
	// MaxServerPerHost caps how many group members may share a host. 0 means
	// unset, which the anti-affinity filter treats as the Nova default of 1.
	MaxServerPerHost int
}

// SchedulerRequest is the well-typed, Nova-agnostic input to the scheduling
// pipeline. Every field is a resolved scalar, enum, or typed collection — there
// are no maps of Nova-specific string keys and no values the pipeline must parse.
//
// Each field documents what leaving it at its zero value means, so the Nova spec
// adapter (and any other caller) can see exactly which fields are load-bearing.
//
// SchedulerRequest implements lib.FilterWeigherPipelineRequest so it can be the
// RequestType of the filter-weigher pipeline.
type SchedulerRequest struct {
	// Pipeline is the name of the pipeline to execute. Required: an empty name
	// resolves no pipeline and the run fails with "pipeline not found".
	Pipeline string
	// GatherAllCandidates, when true, tells the Scheduler to ignore the Hosts and
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
	HypervisorFamily HypervisorFamily
	// HypervisorType is the specific type (qemu/ch/vmware), needed only by Nova
	// side effects. Empty means unresolved; the VM-upsert side effect errors
	// rather than guessing.
	HypervisorType HypervisorType
	// ImageHypervisorType is the hypervisor type declared by the image. The empty
	// value (ImageHypervisorTypeUnset) means the image declared none ("no image
	// constraint"); ImageHypervisorTypeUnparseable means it declared one that
	// could not be parsed, which the image-properties filter surfaces as a
	// diagnostic.
	ImageHypervisorType ImageHypervisorType
	// HardwareVersion is resolved from the "quota:hw_version" extra spec. Empty
	// means no hardware-version constraint.
	HardwareVersion string

	// Flavor is the requested flavor sizing. A zero FlavorSpec means capacity and
	// bin-pack steps see a zero-sized instance (fits anywhere) — the adapter
	// always populates it for real placements.
	Flavor FlavorSpec
	// ImageID is the Glance image UUID. Empty only affects the VM resource
	// written by side effects, not pipeline decisions.
	ImageID string

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
	RequestedCapabilities []Capability
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
	RequestedDestination *RequestedDestination
	// InstanceGroup is the server group the instance belongs to. Nil means the
	// instance is not grouped, so affinity/anti-affinity steps are skipped (see
	// the type doc).
	InstanceGroup *InstanceGroupSpec

	// RequestID identifies this scheduling request for tracing. Empty only
	// affects log correlation.
	RequestID string
	// GlobalRequestID is the cross-service request ID for tracing. Empty only
	// affects log correlation.
	GlobalRequestID string
}

// TODO: REMOVE ME Compile-time check that SchedulerRequest satisfies the pipeline request contract.
var _ lib.FilterWeigherPipelineRequest = SchedulerRequest{}

// GetIntent returns the resolved scheduling intent.
func (r SchedulerRequest) GetIntent() v1alpha1.SchedulingIntent { return r.Intent }

// GetHosts returns the compute host names of all candidates.
func (r SchedulerRequest) GetHosts() []string {
	hosts := make([]string, len(r.Hosts))
	for i, h := range r.Hosts {
		hosts[i] = h.ComputeHost
	}
	return hosts
}

// GetWeights returns the per-host input weights.
func (r SchedulerRequest) GetWeights() map[string]float64 { return r.Weights }

// GetOptions returns the call-time options for this run.
func (r SchedulerRequest) GetOptions() scheduling.Options { return r.Options }

// GetTraceLogArgs returns alternating key/value pairs for the step trace log.
func (r SchedulerRequest) GetTraceLogArgs() []any {
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
func (r SchedulerRequest) Filter(includedHosts map[string]float64) lib.FilterWeigherPipelineRequest {
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
func (r SchedulerRequest) IsForcedDestination() bool {
	if len(r.ForceHosts) == 0 && len(r.ForceNodes) == 0 {
		return false
	}
	return r.Intent == "" || r.Intent == v1alpha1.SchedulingIntentUnknown
}

// ForcedHosts returns the subset of request hosts matching the forced
// destinations. force_hosts matches on compute host name (case-insensitive);
// force_nodes matches on hypervisor hostname (case-sensitive); when both are set
// a host must match both. Order is preserved and compute hosts are deduplicated.
func (r SchedulerRequest) ForcedHosts() []string {
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
