// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import "strings"

// HypervisorFamily is the coarse hypervisor family a request targets. It is the
// distinction the pipeline and request routing care about. The adapter resolves
// the family from Nova's "capabilities:hypervisor_type" flavor extra spec.
type HypervisorFamily string

const (
	// HypervisorFamilyKVM covers KVM-based hypervisors (QEMU and CH).
	HypervisorFamilyKVM HypervisorFamily = "kvm"
	// HypervisorFamilyVMware covers VMware hypervisors.
	HypervisorFamilyVMware HypervisorFamily = "vmware"
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
// pipeline needs into the typed fields on this struct and on PlacementRequest.
type FlavorSpec struct {
	// MemoryMB is the requested memory per instance. 0 means a zero-memory
	// instance to capacity/bin-pack steps (fits anywhere), so the adapter sets it.
	MemoryMB uint64
	// VCPUs is the requested vCPUs per instance. 0 means a zero-CPU instance to
	// capacity/bin-pack steps (fits anywhere), so the adapter sets it.
	VCPUs uint64
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

// The helpers below resolve Nova's loosely-typed flavor extra-spec string
// conventions into the typed values the scheduling pipeline consumes. They live
// here, alongside the typed values they produce, because the
// "trait:"/"capabilities:"/"accel:" key conventions are Nova's — not the
// pipeline's. Both the Nova spec adapter and the in-process reservation
// controllers (which hold a flavor's extra-spec map directly) call them, so the
// conventions are understood in exactly one place.

// unsupportedCapabilityOps are the Nova extra-spec comparison operators the
// capabilities filter does not implement. A capability value containing any of
// these means the request uses an advanced match the pipeline can't honor, so
// SplitCapabilities drops capabilities entirely (the filter then becomes a
// no-op), matching Nova's "skip" behavior.
// See: https://github.com/sapcc/nova/blob/3ebf80/nova/scheduler/filters/extra_specs_ops.py#L23
var unsupportedCapabilityOps = []string{
	"=", "<in>", "<all-in>", "==", "!=", ">=", "<=",
	"s==", "s!=", "s<", "s<=", "s>", "s>=", "<or>",
}

// SplitTraits splits a flavor's "trait:<name>" extra specs into required and
// forbidden trait names, classified by the "required"/"forbidden" value.
func SplitTraits(extraSpecs map[string]string) (required, forbidden []string) {
	for key, value := range extraSpecs {
		name, ok := strings.CutPrefix(key, "trait:")
		if !ok {
			continue
		}
		switch value {
		case "required":
			required = append(required, name)
		case "forbidden":
			forbidden = append(forbidden, name)
		}
	}
	return required, forbidden
}

// SplitCapabilities extracts a flavor's "capabilities:<key>" extra specs as typed
// Capability equalities (prefix stripped). The "hypervisor_type" value is
// canonicalized (qemu->QEMU, ch->CH) so it matches what hypervisors advertise
// (filter_capabilities compares case-sensitively); all other values pass through
// unchanged. If any value uses an unsupported comparison operator it returns nil so
// the capabilities filter is skipped entirely (matching Nova behavior).
func SplitCapabilities(extraSpecs map[string]string) []Capability {
	var caps []Capability
	for key, value := range extraSpecs {
		name, ok := strings.CutPrefix(key, "capabilities:")
		if !ok {
			continue
		}
		for _, op := range unsupportedCapabilityOps {
			if strings.Contains(value, op) {
				return nil // Unsupported operator: skip the whole filter.
			}
		}
		if name == "hypervisor_type" {
			value = CanonicalHypervisorType(value)
		}
		caps = append(caps, Capability{Name: name, Value: value})
	}
	return caps
}

// CanonicalHypervisorType normalizes a flavor's capabilities:hypervisor_type value
// to the casing hypervisors advertise (QEMU/CH), since filter_capabilities compares
// values case-sensitively. Unrecognized values pass through unchanged.
func CanonicalHypervisorType(value string) string {
	switch strings.ToLower(value) {
	case "qemu":
		return "QEMU"
	case "ch":
		return "CH"
	default:
		return value
	}
}

// RequestsAccelerators reports whether a flavor requests a hardware accelerator,
// resolved from the presence of the "accel:device_profile" extra spec.
func RequestsAccelerators(extraSpecs map[string]string) bool {
	_, ok := extraSpecs["accel:device_profile"]
	return ok
}
