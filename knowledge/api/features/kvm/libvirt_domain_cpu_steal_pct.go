// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

type LibvirtDomainCPUStealPct struct {
	// The openstack server instance uuid.
	InstanceUUID string `db:"instance_uuid"`
	// The compute host on which the instance is running.
	Host string `db:"host"`
	// The maximum steal pct recorded.
	MaxStealTimePct float64 `db:"max_steal_time_pct"`
}

// Table under which the feature is stored.
func (LibvirtDomainCPUStealPct) TableName() string {
	return "feature_libvirt_domain_cpu_steal_pct"
}

// Indexes for the feature.
func (LibvirtDomainCPUStealPct) Indexes() map[string][]string { return nil }
