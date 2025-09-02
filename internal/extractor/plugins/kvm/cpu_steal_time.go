// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/internal/sync/prometheus"
)

// Feature that maps CPU steal time of kvm hosts.
type LibvirtDomainCPUStealTime struct {
	Instance string
}

// Table under which the feature is stored.
func (LibvirtDomainCPUStealTime) TableName() string {
	return "feature_libvirt_domain_cpu_steal_time"
}

// Indexes for the feature.
func (LibvirtDomainCPUStealTime) Indexes() []db.Index {
	return nil
}

// Extractor that extracts CPU steal time
type LibvirtDomainCPUStealTimeExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                  // No options passed through yaml config
		LibvirtDomainCPUStealTime, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*LibvirtDomainCPUStealTimeExtractor) GetName() string {
	return "libvirt_domain_cpu_steal_time_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (LibvirtDomainCPUStealTimeExtractor) Triggers() []string {
	return []string{
		prometheus.TriggerMetricAliasSynced("kvm_libvirt_domain_steal_time"),
	}
}

//go:embed cpu_steal_time.sql
var cpuStealTimeSQL string

// Extract CPU steal time of kvm hosts.
func (e *LibvirtDomainCPUStealTimeExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(cpuStealTimeSQL)
}
