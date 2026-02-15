// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins"
)

type LibvirtDomainCPUStealPct struct {
	// The openstack server instance uuid.
	InstanceUUID string `db:"instance_uuid"`
	// The compute host on which the instance is running.
	Host string `db:"host"`
	// The maximum steal pct recorded.
	MaxStealTimePct float64 `db:"max_steal_time_pct"`
}

// Extractor that extracts CPU steal percentage of kvm instances and stores
// it as a feature into the database.
type LibvirtDomainCPUStealPctExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                 // No options passed through yaml config
		LibvirtDomainCPUStealPct, // Feature model
	]
}

//go:embed libvirt_domain_cpu_steal_pct.sql
var libvirtDomainCPUStealPctSQL string

// Extract CPU steal time of kvm hosts.
func (e *LibvirtDomainCPUStealPctExtractor) Extract(_ []*v1alpha1.Datasource, _ []*v1alpha1.Knowledge) ([]plugins.Feature, error) {
	return e.ExtractSQL(libvirtDomainCPUStealPctSQL)
}
