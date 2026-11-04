// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"strings"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

// ToSchedulerRequest translates the Nova over-the-wire request into the typed,
// Nova-agnostic PlacementRequest the pipeline consumes. This is the one
// place that reads Nova's loosely-typed scheduler hints and flavor/image extra
// specs; every downstream filter/weigher sees only resolved typed fields.
//
// intent and pipeline are resolved by the caller (the HTTP API layer) and passed
// in.
func ToSchedulerRequest(
	wire api.ExternalSchedulerRequest,
	intent v1alpha1.SchedulingIntent,
	pipeline string,
) PlacementRequest {

	spec := wire.Spec.Data

	req := PlacementRequest{
		Pipeline: pipeline,
		Hosts:    toComputeHosts(wire.Hosts),
		Weights:  wire.Weights,
		Options:  wire.Options,
		Intent:   intent,

		InstanceUUID:     spec.InstanceUUID,
		ProjectID:        firstNonEmpty(spec.ProjectID, wire.Context.ProjectID),
		UserID:           spec.UserID,
		AvailabilityZone: spec.AvailabilityZone,
		NumInstances:     spec.NumInstances,

		IgnoreHosts: derefStrings(spec.IgnoreHosts),
		ForceHosts:  derefStrings(spec.ForceHosts),
		ForceNodes:  derefStrings(spec.ForceNodes),

		HardwareVersion: spec.Flavor.Data.ExtraSpecs["quota:hw_version"],

		SourceHost:    hintStr(spec, "source_host"),
		DomainName:    hintStr(spec, "domain_name"),
		ResourceGroup: hintStr(spec, api.HintKeyResourceGroup),

		RequestID:       wire.Context.RequestID,
		GlobalRequestID: derefString(wire.Context.GlobalRequestID),
	}

	// Resource sizing.
	req.RequestedResources = NewResourceDemand(spec.Flavor.Data.VCPUs, spec.Flavor.Data.MemoryMB)

	// Hypervisor family.
	req.HypervisorFamily = toHypervisorFamily(wire)

	// Whether to gather all placement candidates (ignore the caller's host
	// preselection) is determined by the hypervisor family: cortex can enumerate
	// KVM hypervisors itself, whereas VMware placement relies on the host list
	// Nova sent in the request.
	req.GatherAllCandidates = req.HypervisorFamily == HypervisorFamilyKVM

	// Image.
	req.ImageHypervisorType = toImageHypervisorType(spec.Image.Data)

	// Traits, capabilities, accelerators from flavor extra specs.
	req.RequiredTraits, req.ForbiddenTraits = SplitTraits(spec.Flavor.Data.ExtraSpecs)
	req.RequestedCapabilities = SplitCapabilities(spec.Flavor.Data.ExtraSpecs)
	req.AcceleratorsRequested = RequestsAccelerators(spec.Flavor.Data.ExtraSpecs)

	// Nested objects.
	req.RequestedDestination = toRequestedDestination(spec.RequestedDestination)
	req.InstanceGroup = toInstanceGroup(spec.InstanceGroup)

	return req
}

func toComputeHosts(hosts []api.ExternalSchedulerHost) []Host {
	out := make([]Host, len(hosts))
	for i, h := range hosts {
		out[i] = Host{ComputeHost: h.ComputeHost, HypervisorHostname: h.HypervisorHostname}
	}
	return out
}

// toHypervisorFamily resolves the coarse family from the wire hypervisor type.
// It is empty when unresolved.
func toHypervisorFamily(wire api.ExternalSchedulerRequest) HypervisorFamily {
	hv, err := wire.GetHypervisorType()
	if err != nil {
		return ""
	}
	switch hv {
	case api.HypervisorTypeQEMU, api.HypervisorTypeCH:
		return HypervisorFamilyKVM
	case api.HypervisorTypeVMware:
		return HypervisorFamilyVMware
	}
	return ""
}

// toImageHypervisorType maps the image-declared hypervisor type. It returns
// ImageHypervisorTypeUnparseable when the image declared a type that could not
// be parsed, distinct from ImageHypervisorTypeUnset when none was declared.
func toImageHypervisorType(image api.NovaImageMeta) ImageHypervisorType {
	hv, err := image.GetHypervisorType()
	if err != nil {
		// GetHypervisorType errors both when nothing is declared and when a
		// declared value is unrecognized. Distinguish them: if a known key is
		// present, the value was unparseable.
		if image.Properties.Data != nil {
			for _, key := range []string{"img_hv_type", "hypervisor_type"} {
				if _, ok := image.Properties.Data[key]; ok {
					return ImageHypervisorTypeUnparseable
				}
			}
		}
		return ImageHypervisorTypeUnset
	}
	switch hv {
	case api.NovaImageMetaHVTypeVMware:
		return ImageHypervisorTypeVMware
	case api.NovaImageMetaHVTypeBaremetal:
		return ImageHypervisorTypeBaremetal
	case api.NovaImageMetaHVTypeKVM:
		return ImageHypervisorTypeKVM
	}
	return ImageHypervisorTypeUnset
}

func toRequestedDestination(rd *api.NovaObject[api.NovaRequestedDestination]) *RequestedDestination {
	if rd == nil {
		return nil
	}
	return &RequestedDestination{
		Host:       rd.Data.Host,
		Aggregates: rd.Data.Aggregates,
	}
}

func toInstanceGroup(ig *api.NovaObject[api.NovaInstanceGroup]) *InstanceGroupSpec {
	if ig == nil {
		return nil
	}
	out := &InstanceGroupSpec{
		UUID:    ig.Data.UUID,
		Policy:  ig.Data.Policy,
		Hosts:   ig.Data.Hosts,
		Members: ig.Data.Members,
	}
	if ig.Data.Rules != nil {
		if v, ok := ig.Data.Rules["max_server_per_host"]; ok {
			if n, ok := v.(int); ok {
				out.MaxServerPerHost = n
			}
		}
	}
	return out
}

func hintStr(spec api.NovaSpec, key string) string {
	s, err := spec.GetSchedulerHintStr(key)
	if err != nil {
		return ""
	}
	return s
}

func derefStrings(p *[]string) []string {
	if p == nil {
		return nil
	}
	return *p
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// The helpers below resolve Nova's loosely-typed flavor extra-spec string
// conventions into the typed values the scheduling pipeline consumes. They live
// here, with the Nova spec adapter, because the "trait:"/"capabilities:"/"accel:"
// key conventions are Nova's — not the pipeline's. Both this adapter and the
// in-process reservation controllers (which hold a flavor's extra-spec map
// directly) call them, so the conventions are understood in exactly one place.

// NewResourceDemand builds a ResourceDemand from Nova's vCPU count and memory in
// mebibytes. Nova's memory_mb field is documented as "megabytes" but its values are
// mebibytes (1 MiB = 2^20 bytes); this converts to the bytes hv.Status and the
// capacity lib use, so the Nova unit convention is applied in exactly one place.
// Resources left at 0 are still written, so callers can rely on the keys being present.
func NewResourceDemand(vcpus, memoryMB uint64) ResourceDemand {
	//nolint:gosec // vCPU and memory values are small, non-negative flavor counts.
	return ResourceDemand{
		hv1.ResourceCPU:    *resource.NewQuantity(int64(vcpus), resource.DecimalSI),
		hv1.ResourceMemory: *resource.NewQuantity(int64(memoryMB)*(1<<20), resource.BinarySI),
	}
}

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
