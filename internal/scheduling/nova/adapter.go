// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
)

// ToSchedulerRequest translates the Nova over-the-wire request into the typed,
// Nova-agnostic compute.PlacementRequest the pipeline consumes. This is the one
// place that reads Nova's loosely-typed scheduler hints and flavor/image extra
// specs; every downstream filter/weigher sees only resolved typed fields.
//
// intent and pipeline are resolved by the caller (the HTTP API layer) and passed
// in.
func ToSchedulerRequest(
	wire api.ExternalSchedulerRequest,
	intent v1alpha1.SchedulingIntent,
	pipeline string,
) compute.PlacementRequest {

	spec := wire.Spec.Data

	req := compute.PlacementRequest{
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

	// Flavor sizing.
	req.Flavor = api.FlavorSpec{
		VCPUs:    spec.Flavor.Data.VCPUs,
		MemoryMB: spec.Flavor.Data.MemoryMB,
	}

	// Hypervisor family.
	req.HypervisorFamily = toHypervisorFamily(wire)

	// Whether to gather all placement candidates (ignore the caller's host
	// preselection) is determined by the hypervisor family: cortex can enumerate
	// KVM hypervisors itself, whereas VMware placement relies on the host list
	// Nova sent in the request.
	req.GatherAllCandidates = req.HypervisorFamily == api.HypervisorFamilyKVM

	// Image.
	req.ImageHypervisorType = toImageHypervisorType(spec.Image.Data)

	// Traits, capabilities, accelerators from flavor extra specs.
	req.RequiredTraits, req.ForbiddenTraits = api.SplitTraits(spec.Flavor.Data.ExtraSpecs)
	req.RequestedCapabilities = api.SplitCapabilities(spec.Flavor.Data.ExtraSpecs)
	req.AcceleratorsRequested = api.RequestsAccelerators(spec.Flavor.Data.ExtraSpecs)

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

// toHypervisorFamily resolves the coarse family from the wire hypervisor type.
// It is empty when unresolved.
func toHypervisorFamily(wire api.ExternalSchedulerRequest) api.HypervisorFamily {
	hv, err := wire.GetHypervisorType()
	if err != nil {
		return ""
	}
	switch hv {
	case api.HypervisorTypeQEMU, api.HypervisorTypeCH:
		return api.HypervisorFamilyKVM
	case api.HypervisorTypeVMware:
		return api.HypervisorFamilyVMware
	}
	return ""
}

// toImageHypervisorType maps the image-declared hypervisor type. It returns
// ImageHypervisorTypeUnparseable when the image declared a type that could not
// be parsed, distinct from ImageHypervisorTypeUnset when none was declared.
func toImageHypervisorType(image api.NovaImageMeta) api.ImageHypervisorType {
	hv, err := image.GetHypervisorType()
	if err != nil {
		// GetHypervisorType errors both when nothing is declared and when a
		// declared value is unrecognized. Distinguish them: if a known key is
		// present, the value was unparseable.
		if image.Properties.Data != nil {
			for _, key := range []string{"img_hv_type", "hypervisor_type"} {
				if _, ok := image.Properties.Data[key]; ok {
					return api.ImageHypervisorTypeUnparseable
				}
			}
		}
		return api.ImageHypervisorTypeUnset
	}
	switch hv {
	case api.NovaImageMetaHVTypeVMware:
		return api.ImageHypervisorTypeVMware
	case api.NovaImageMetaHVTypeBaremetal:
		return api.ImageHypervisorTypeBaremetal
	case api.NovaImageMetaHVTypeKVM:
		return api.ImageHypervisorTypeKVM
	}
	return api.ImageHypervisorTypeUnset
}

func toRequestedDestination(rd *api.NovaObject[api.NovaRequestedDestination]) *api.RequestedDestination {
	if rd == nil {
		return nil
	}
	return &api.RequestedDestination{
		Host:       rd.Data.Host,
		Aggregates: rd.Data.Aggregates,
	}
}

func toInstanceGroup(ig *api.NovaObject[api.NovaInstanceGroup]) *api.InstanceGroupSpec {
	if ig == nil {
		return nil
	}
	out := &api.InstanceGroupSpec{
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
