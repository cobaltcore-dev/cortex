// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"context"
	_ "embed"
	"errors"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Feature that maps CPU contention of vROps hostsystems.
type VROpsHostsystemContentionShortTerm struct {
	ComputeHost      string
	AvgCPUContention float64
	MaxCPUContention float64
}

// Extractor that extracts CPU contention of vROps hostsystems and stores
// it as a feature into the database.
type VROpsHostsystemContentionShortTermExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                           // No options passed through yaml config
		VROpsHostsystemContentionShortTerm, // Feature model
	]
}

//go:embed vrops_hostsystem_contention_short_term.sql
var vropsHostsystemContentionShortTermSQL string

// Extract short term CPU contention of hostsystems.
// Depends on resolved vROps hostsystems (feature_vrops_resolved_hostsystem).
func (e *VROpsHostsystemContentionShortTermExtractor) Extract() ([]plugins.Feature, error) {
	if e.DB == nil {
		return nil, errors.New("database connection is not initialized")
	}
	type queryResult struct {
		Hostsystem       string  `db:"vrops_hostsystem"`
		AvgCPUContention float64 `db:"avg_cpu_contention"`
		MaxCPUContention float64 `db:"max_cpu_contention"`
	}
	var unresolved []queryResult
	if _, err := e.DB.Select(&unresolved, vropsHostsystemContentionShortTermSQL); err != nil {
		return nil, err
	}

	resolvedHostsystemsKnowledge := &v1alpha1.Knowledge{}
	if err := e.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "vmware-resolved-hostsystems"},
		resolvedHostsystemsKnowledge,
	); err != nil {
		return nil, err
	}
	resolvedHostsystems, err := v1alpha1.
		UnboxFeatureList[ResolvedVROpsHostsystem](resolvedHostsystemsKnowledge.Status.Raw)
	if err != nil {
		return nil, err
	}
	hostsystemToComputeHost := make(map[string]string)
	for _, rh := range resolvedHostsystems {
		hostsystemToComputeHost[rh.VROpsHostsystem] = rh.NovaComputeHost
	}

	avgsByComputeHost := make(map[string][]float64)
	maxsByComputeHost := make(map[string][]float64)
	for _, ur := range unresolved {
		computeHost, ok := hostsystemToComputeHost[ur.Hostsystem]
		if !ok {
			slog.Warn("skipping unresolved hostsystem", "hostsystem", ur.Hostsystem)
			continue
		}
		avgsByComputeHost[computeHost] = append(avgsByComputeHost[computeHost], ur.AvgCPUContention)
		maxsByComputeHost[computeHost] = append(maxsByComputeHost[computeHost], ur.MaxCPUContention)
	}

	var features []VROpsHostsystemContentionShortTerm
	for computeHost, avgContentions := range avgsByComputeHost {
		if len(avgContentions) == 0 {
			slog.Warn("no avg cpu contentions for compute host", "compute_host", computeHost)
			continue
		}
		var sumContentions float64 = 0
		var maxContention float64 = 0
		for _, v := range avgContentions {
			sumContentions += v
		}
		for _, v := range maxsByComputeHost[computeHost] {
			if v > maxContention {
				maxContention = v
			}
		}
		avg := sumContentions / float64(len(avgContentions))
		features = append(features, VROpsHostsystemContentionShortTerm{
			ComputeHost:      computeHost,
			AvgCPUContention: avg,
			MaxCPUContention: maxContention,
		})
	}

	return e.Extracted(features)
}
