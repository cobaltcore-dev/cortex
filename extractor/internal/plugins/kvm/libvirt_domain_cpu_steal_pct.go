// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/extractor/api/features/kvm"
	"github.com/cobaltcore-dev/cortex/extractor/internal/plugins"
	"github.com/cobaltcore-dev/cortex/sync/api/objects/prometheus"
)

// Extractor that extracts CPU steal percentage of kvm instances and stores
// it as a feature into the database.
type LibvirtDomainCPUStealPctExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                     // No options passed through yaml config
		kvm.LibvirtDomainCPUStealPct, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*LibvirtDomainCPUStealPctExtractor) GetName() string {
	return "kvm_libvirt_domain_cpu_steal_pct_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (LibvirtDomainCPUStealPctExtractor) Triggers() []string {
	return []string{
		prometheus.TriggerMetricAliasSynced("kvm_libvirt_domain_steal_pct"),
	}
}

//go:embed libvirt_domain_cpu_steal_pct.sql
var libvirtDomainCPUStealPctSQL string

// Extract CPU steal time of kvm hosts.
func (e *LibvirtDomainCPUStealPctExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(libvirtDomainCPUStealPctSQL)
}
