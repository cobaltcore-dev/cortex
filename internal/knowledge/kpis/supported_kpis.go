// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kpis

import (
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins/storage"
)

// Configuration of supported kpis.
var supportedKPIs = map[string]plugins.KPI{
	"kvm_host_capacity_kpi":               &compute.HostCapacityKPI{},
	"vmware_host_contention_kpi":          &compute.VMwareHostContentionKPI{},
	"vmware_project_noisiness_kpi":        &compute.VMwareProjectNoisinessKPI{},
	"host_total_allocatable_capacity_kpi": &compute.HostTotalAllocatableCapacityKPI{},
	"host_capacity_kpi":                   &compute.HostAvailableCapacityKPI{},
	"host_running_vms_kpi":                &compute.HostRunningVMsKPI{},
	"flavor_running_vms_kpi":              &compute.FlavorRunningVMsKPI{},
	"vm_migration_statistics_kpi":         &compute.VMMigrationStatisticsKPI{},
	"vm_life_span_kpi":                    &compute.VMLifeSpanKPI{},
	"vm_commitments_kpi":                  &compute.VMCommitmentsKPI{},

	"netapp_storage_pool_cpu_usage_kpi": &storage.NetAppStoragePoolCPUUsageKPI{},
}
