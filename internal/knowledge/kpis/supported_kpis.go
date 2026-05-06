// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package kpis

import (
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins/deployment"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins/infrastructure"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins/storage"
)

// Configuration of supported kpis.
var supportedKPIs = map[string]plugins.KPI{
	"vmware_host_contention_kpi":   &compute.VMwareHostContentionKPI{},
	"vmware_project_noisiness_kpi": &compute.VMwareProjectNoisinessKPI{},
	"host_running_vms_kpi":         &compute.HostRunningVMsKPI{},
	"flavor_running_vms_kpi":       &compute.FlavorRunningVMsKPI{},
	"vm_migration_statistics_kpi":  &compute.VMMigrationStatisticsKPI{},
	"vm_life_span_kpi":             &compute.VMLifeSpanKPI{},
	"vm_commitments_kpi":           &compute.VMCommitmentsKPI{},
	"vm_faults_kpi":                &compute.VMFaultsKPI{},

	"kvm_host_capacity_kpi":          &infrastructure.KVMHostCapacityKPI{},
	"kvm_project_utilization_kpi":    &infrastructure.KVMProjectUtilizationKPI{},
	"vmware_project_utilization_kpi": &infrastructure.VMwareProjectUtilizationKPI{},
	"vmware_project_commitments_kpi": &infrastructure.VMwareProjectCommitmentsKPI{},
	"vmware_host_capacity_kpi":       &infrastructure.VMwareHostCapacityKPI{},

	"netapp_storage_pool_cpu_usage_kpi": &storage.NetAppStoragePoolCPUUsageKPI{},

	"datasource_state_kpi": &deployment.DatasourceStateKPI{},
	"knowledge_state_kpi":  &deployment.KnowledgeStateKPI{},
	"decision_state_kpi":   &deployment.DecisionStateKPI{},
	"kpi_state_kpi":        &deployment.KPIStateKPI{},
	"pipeline_state_kpi":   &deployment.PipelineStateKPI{},
}
