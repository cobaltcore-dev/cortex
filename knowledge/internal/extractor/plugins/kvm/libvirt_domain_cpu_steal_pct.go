// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/knowledge/api/features/kvm"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/extractor/plugins"
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

//go:embed libvirt_domain_cpu_steal_pct.sql
var libvirtDomainCPUStealPctSQL string

// Extract CPU steal time of kvm hosts.
func (e *LibvirtDomainCPUStealPctExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(libvirtDomainCPUStealPctSQL)
}
