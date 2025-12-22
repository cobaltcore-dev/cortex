// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kpis

import (
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins/kvm"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins/netapp"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins/sap"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins/vmware"
)

// Configuration of supported kpis.
var supportedKPIs = map[string]plugins.KPI{
	// KVM kpis.
	"kvm_host_capacity_kpi": &kvm.HostCapacityKPI{},
	// VMware kpis.
	"vmware_host_contention_kpi":   &vmware.VMwareHostContentionKPI{},
	"vmware_project_noisiness_kpi": &vmware.VMwareProjectNoisinessKPI{},
	// NetApp kpis.
	"netapp_storage_pool_cpu_usage_kpi": &netapp.NetAppStoragePoolCPUUsageKPI{},
	// Shared kpis.
	"vm_migration_statistics_kpi": &shared.VMMigrationStatisticsKPI{},
	"vm_life_span_kpi":            &shared.VMLifeSpanKPI{},
	"vm_commitments_kpi":          &shared.VMCommitmentsKPI{},
	// SAP kpis.
	"sap_host_total_allocatable_capacity_kpi": &sap.HostTotalAllocatableCapacityKPI{},
	"sap_host_capacity_kpi":                   &sap.HostAvailableCapacityKPI{},
	"sap_host_running_vms_kpi":                &sap.HostRunningVMsKPI{},
	"sap_flavor_running_vms_kpi":              &sap.FlavorRunningVMsKPI{},
}
