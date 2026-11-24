// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"context"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/extractor"
)

// Feature that maps short term CPU contention of vROps hostsystems.
type VROpsHostsystemContentionShortTerm struct {
	ComputeHost      string
	AvgCPUContention float64
	MaxCPUContention float64
}

// Extractor that extracts short term CPU contention of vROps hostsystems.
type VROpsHostsystemContentionShortTermExtractor struct {
	// Common base for all extractors that provides standard functionality.
	extractor.BaseExtractor[struct{}]
}

// Extract short term CPU contention of the compute hosts.
func (e *VROpsHostsystemContentionShortTermExtractor) Extract(_ context.Context, in VROpsHostsystemContentionInputs) ([]VROpsHostsystemContentionShortTerm, error) {
	contentions := hostContentions(in)
	features := make([]VROpsHostsystemContentionShortTerm, len(contentions))
	for i, c := range contentions {
		features[i] = VROpsHostsystemContentionShortTerm{
			ComputeHost:      c.computeHost,
			AvgCPUContention: c.avgCPUContention,
			MaxCPUContention: c.maxCPUContention,
		}
	}
	return features, nil
}

func init() {
	extractor.Register("vrops_hostsystem_contention_short_term_extractor", func() extractor.Extractor[VROpsHostsystemContentionInputs, VROpsHostsystemContentionShortTerm] {
		return &VROpsHostsystemContentionShortTermExtractor{}
	})
}
