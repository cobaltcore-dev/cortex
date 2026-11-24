// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"context"
	"slices"
	"strings"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/extractor"
)

// Feature that maps the availability zone of a compute host.
type HostAZ struct {
	// Name of the OpenStack compute host.
	ComputeHost string `db:"compute_host"`
	// Availability zone of the compute host, if available.
	AvailabilityZone *string `db:"availability_zone"`
}

// Inputs of the host availability zone extractor.
type HostAZInputs struct {
	Hypervisors []nova.Hypervisor `input:"hypervisors"`
	Aggregates  []nova.Aggregate  `input:"aggregates"`
}

// Extractor that extracts the availability zone of each compute host.
type HostAZExtractor struct {
	// Common base for all extractors that provides standard functionality.
	extractor.BaseExtractor[struct{}]
}

// Extract the availability zone of each hypervisor's compute host from the
// aggregates the host belongs to. If the aggregates of a host define multiple
// availability zones, the lexicographically smallest one is used.
func (e *HostAZExtractor) Extract(_ context.Context, in HostAZInputs) ([]HostAZ, error) {
	azByHost := make(map[string]string)
	for _, a := range in.Aggregates {
		if a.ComputeHost == nil || a.AvailabilityZone == nil {
			continue
		}
		if az, ok := azByHost[*a.ComputeHost]; !ok || *a.AvailabilityZone < az {
			azByHost[*a.ComputeHost] = *a.AvailabilityZone
		}
	}
	features := make([]HostAZ, 0, len(in.Hypervisors))
	for _, h := range in.Hypervisors {
		f := HostAZ{ComputeHost: h.ServiceHost}
		if az, ok := azByHost[h.ServiceHost]; ok {
			f.AvailabilityZone = &az
		}
		features = append(features, f)
	}
	slices.SortStableFunc(features, func(a, b HostAZ) int {
		return strings.Compare(a.ComputeHost, b.ComputeHost)
	})
	return features, nil
}

func init() {
	extractor.Register("host_az_extractor", func() extractor.Extractor[HostAZInputs, HostAZ] {
		return &HostAZExtractor{}
	})
}
