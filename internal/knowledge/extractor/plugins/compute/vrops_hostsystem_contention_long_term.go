// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"context"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/extractor"
)

// Feature that maps long term CPU contention of vROps hostsystems.
type VROpsHostsystemContentionLongTerm struct {
	ComputeHost      string
	AvgCPUContention float64
	MaxCPUContention float64
}

// Extractor that extracts long term CPU contention of vROps hostsystems.
type VROpsHostsystemContentionLongTermExtractor struct {
	// Common base for all extractors that provides standard functionality.
	extractor.BaseExtractor[struct{}]
}

// Extract long term CPU contention of the compute hosts.
func (e *VROpsHostsystemContentionLongTermExtractor) Extract(_ context.Context, in VROpsHostsystemContentionInputs) ([]VROpsHostsystemContentionLongTerm, error) {
	contentions := hostContentions(in)
	features := make([]VROpsHostsystemContentionLongTerm, len(contentions))
	for i, c := range contentions {
		features[i] = VROpsHostsystemContentionLongTerm{
			ComputeHost:      c.computeHost,
			AvgCPUContention: c.avgCPUContention,
			MaxCPUContention: c.maxCPUContention,
		}
	}
	return features, nil
}

func init() {
	extractor.Register("vrops_hostsystem_contention_long_term_extractor", func() extractor.Extractor[VROpsHostsystemContentionInputs, VROpsHostsystemContentionLongTerm] {
		return &VROpsHostsystemContentionLongTermExtractor{}
	})
}
