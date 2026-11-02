// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"strings"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"k8s.io/apimachinery/pkg/api/resource"
)

// unsupportedCapabilityOps are the Nova extra-spec comparison operators the
// capabilities filter does not implement. A capability value containing any of
// these means the request uses an advanced match the pipeline can't honor, so
// we drop capabilities entirely (the filter then becomes a no-op), matching the
// old filter's "skip" behavior.
// See: https://github.com/sapcc/nova/blob/3ebf80/nova/scheduler/filters/extra_specs_ops.py#L23
var unsupportedCapabilityOps = []string{
	"=", "<in>", "<all-in>", "==", "!=", ">=", "<=",
	"s==", "s!=", "s<", "s<=", "s>", "s>=", "<or>",
}

// ToSchedulerRequest translates the Nova over-the-wire request into the typed,
// Nova-agnostic compute.SchedulerRequest the pipeline consumes. This is the one
// place that reads Nova's loosely-typed scheduler hints and flavor/image extra
// specs; every downstream filter/weigher sees only resolved typed fields.
//
// intent and pipeline are resolved by the caller (the HTTP API layer) and passed
// in.
func ToSchedulerRequest(
	wire api.ExternalSchedulerRequest,
	intent v1alpha1.SchedulingIntent,
	pipeline string,
) compute.SchedulerRequest {

	spec := wire.Spec.Data

	req := compute.SchedulerRequest{
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

	// Flavor sizing + classification.
	req.Flavor = compute.FlavorSpec{
		Name:     spec.Flavor.Data.Name,
		FlavorID: spec.Flavor.Data.FlavorID,
		VCPUs:    spec.Flavor.Data.VCPUs,
		// Nova's memory_mb is in mebibytes (2^20 bytes).
		Memory: *resource.NewQuantity(int64(spec.Flavor.Data.MemoryMB)*(1<<20), resource.BinarySI), //nolint:gosec // flavor memory bounded by Nova
		Type:   toFlavorType(wire),
	}

	// Hypervisor family + specific type.
	req.HypervisorFamily, req.HypervisorType = toHypervisorFamilyAndType(wire)

	// Whether to gather all placement candidates (ignore the caller's host
	// preselection) is determined by the hypervisor family: cortex can enumerate
	// KVM hypervisors itself, whereas VMware placement relies on the host list
	// Nova sent in the request.
	req.GatherAllCandidates = req.HypervisorFamily == compute.HypervisorFamilyKVM

	// Image.
	req.ImageID = spec.Image.Data.ID
	req.ImageHypervisorType = toImageHypervisorType(spec.Image.Data)

	// Traits, capabilities, accelerators from flavor extra specs.
	req.RequiredTraits, req.ForbiddenTraits = toTraits(spec.Flavor.Data.ExtraSpecs)
	req.RequestedCapabilities = toCapabilities(spec.Flavor.Data.ExtraSpecs)
	_, req.AcceleratorsRequested = spec.Flavor.Data.ExtraSpecs["accel:device_profile"]

	// Nested objects.
	req.RequestedDestination = toRequestedDestination(spec.RequestedDestination)
	req.InstanceGroup = toInstanceGroup(spec.InstanceGroup)

	return req
}

func toComputeHosts(hosts []api.ExternalSchedulerHost) []compute.Host {
	out := make([]compute.Host, len(hosts))
	for i, h := range hosts {
		out[i] = compute.Host{ComputeHost: h.ComputeHost, HypervisorHostname: h.HypervisorHostname}
	}
	return out
}

// toFlavorType maps the wire flavor type enum onto the compute enum.
func toFlavorType(wire api.ExternalSchedulerRequest) compute.FlavorType {
	ft, err := wire.GetFlavorType()
	if err != nil {
		return compute.FlavorTypeGeneralPurpose
	}
	if ft == api.FlavorTypeHANA {
		return compute.FlavorTypeHANA
	}
	return compute.FlavorTypeGeneralPurpose
}

// toHypervisorFamilyAndType resolves the coarse family and the specific type
// from the wire hypervisor type. Both are empty when unresolved.
func toHypervisorFamilyAndType(wire api.ExternalSchedulerRequest) (compute.HypervisorFamily, compute.HypervisorType) {
	hv, err := wire.GetHypervisorType()
	if err != nil {
		return "", ""
	}
	switch hv {
	case api.HypervisorTypeQEMU:
		return compute.HypervisorFamilyKVM, compute.HypervisorTypeQEMU
	case api.HypervisorTypeCH:
		return compute.HypervisorFamilyKVM, compute.HypervisorTypeCH
	case api.HypervisorTypeVMware:
		return compute.HypervisorFamilyVMware, compute.HypervisorTypeVMware
	}
	return "", ""
}

// toImageHypervisorType maps the image-declared hypervisor type. It returns
// ImageHypervisorTypeUnparseable when the image declared a type that could not
// be parsed, distinct from ImageHypervisorTypeUnset when none was declared.
func toImageHypervisorType(image api.NovaImageMeta) compute.ImageHypervisorType {
	hv, err := image.GetHypervisorType()
	if err != nil {
		// GetHypervisorType errors both when nothing is declared and when a
		// declared value is unrecognized. Distinguish them: if a known key is
		// present, the value was unparseable.
		if image.Properties.Data != nil {
			for _, key := range []string{"img_hv_type", "hypervisor_type"} {
				if _, ok := image.Properties.Data[key]; ok {
					return compute.ImageHypervisorTypeUnparseable
				}
			}
		}
		return compute.ImageHypervisorTypeUnset
	}
	switch hv {
	case api.NovaImageMetaHVTypeVMware:
		return compute.ImageHypervisorTypeVMware
	case api.NovaImageMetaHVTypeBaremetal:
		return compute.ImageHypervisorTypeBaremetal
	case api.NovaImageMetaHVTypeKVM:
		return compute.ImageHypervisorTypeKVM
	}
	return compute.ImageHypervisorTypeUnset
}

// toTraits splits the flavor extra specs' "trait:<name>" entries into required
// and forbidden trait names (classified by the "required"/"forbidden" value).
func toTraits(extraSpecs map[string]string) (required, forbidden []string) {
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

// toCapabilities extracts the "capabilities:<key>" extra specs as typed Capability
// equalities. If any value uses an unsupported comparison operator, it returns
// nil so the capabilities filter is skipped entirely (matching Nova behavior).
func toCapabilities(extraSpecs map[string]string) []compute.Capability {
	var caps []compute.Capability
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
		caps = append(caps, compute.Capability{Name: name, Value: value})
	}
	return caps
}

func toRequestedDestination(rd *api.NovaObject[api.NovaRequestedDestination]) *compute.RequestedDestination {
	if rd == nil {
		return nil
	}
	return &compute.RequestedDestination{
		Host:       rd.Data.Host,
		Aggregates: rd.Data.Aggregates,
	}
}

func toInstanceGroup(ig *api.NovaObject[api.NovaInstanceGroup]) *compute.InstanceGroupSpec {
	if ig == nil {
		return nil
	}
	out := &compute.InstanceGroupSpec{
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
