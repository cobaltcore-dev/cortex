// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/placement"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/extractor"
)

// Feature that maps the traits of a compute host in OpenStack.
type HostCapabilities struct {
	// Name of the OpenStack compute host.
	ComputeHost string `db:"compute_host"`
	// Comma-separated list of traits assigned to the compute host.
	Traits string `db:"traits"`
	// The type of hypervisor running on the compute host.
	HypervisorType string `db:"hypervisor_type"`
}

// Inputs of the host capabilities extractor.
type HostCapabilitiesInputs struct {
	Hypervisors []nova.Hypervisor `input:"hypervisors"`
	Traits      []placement.Trait `input:"traits"`
}

// Extractor that extracts the traits of a compute host.
type HostCapabilitiesExtractor struct {
	// Common base for all extractors that provides standard functionality.
	extractor.BaseExtractor[struct{}]
}

// Extract the traits of each compute host and hypervisor type. The traits of
// all hypervisors of a compute host are combined in one sorted comma-separated
// list. Hypervisors without traits are skipped.
func (e *HostCapabilitiesExtractor) Extract(_ context.Context, in HostCapabilitiesInputs) ([]HostCapabilities, error) {
	// The hypervisor id corresponds to the resource provider uuid.
	traitsByProvider := make(map[string][]string)
	for _, t := range in.Traits {
		traitsByProvider[t.ResourceProviderUUID] = append(traitsByProvider[t.ResourceProviderUUID], t.Name)
	}
	type key struct{ computeHost, hypervisorType string }
	traitsByHost := make(map[key][]string)
	for _, h := range in.Hypervisors {
		traits, ok := traitsByProvider[h.ID]
		if !ok {
			continue
		}
		k := key{h.ServiceHost, h.HypervisorType}
		traitsByHost[k] = append(traitsByHost[k], traits...)
	}
	features := make([]HostCapabilities, 0, len(traitsByHost))
	for k, traits := range traitsByHost {
		slices.Sort(traits)
		features = append(features, HostCapabilities{
			ComputeHost:    k.computeHost,
			Traits:         strings.Join(traits, ","),
			HypervisorType: k.hypervisorType,
		})
	}
	slices.SortFunc(features, func(a, b HostCapabilities) int {
		return cmp.Or(cmp.Compare(a.ComputeHost, b.ComputeHost), cmp.Compare(a.HypervisorType, b.HypervisorType))
	})
	return features, nil
}

func init() {
	extractor.Register("host_capabilities_extractor", func() extractor.Extractor[HostCapabilitiesInputs, HostCapabilities] {
		return &HostCapabilitiesExtractor{}
	})
}
