// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/storage"
)

// All supported feature extractors.
var supportedExtractors = map[string]plugins.FeatureExtractor{
	"vrops_hostsystem_resolver":                        &compute.VROpsHostsystemResolver{},
	"vrops_project_noisiness_extractor":                &compute.VROpsProjectNoisinessExtractor{},
	"vrops_hostsystem_contention_long_term_extractor":  &compute.VROpsHostsystemContentionLongTermExtractor{},
	"vrops_hostsystem_contention_short_term_extractor": &compute.VROpsHostsystemContentionShortTermExtractor{},
	"kvm_libvirt_domain_cpu_steal_pct_extractor":       &compute.LibvirtDomainCPUStealPctExtractor{},
	"host_utilization_extractor":                       &compute.HostUtilizationExtractor{},
	"host_capabilities_extractor":                      &compute.HostCapabilitiesExtractor{},
	"vm_host_residency_extractor":                      &compute.VMHostResidencyExtractor{},
	"vm_life_span_histogram_extractor":                 &compute.VMLifeSpanHistogramExtractor{},
	"host_az_extractor":                                &compute.HostAZExtractor{},
	"host_pinned_projects_extractor":                   &compute.HostPinnedProjectsExtractor{},
	"sap_host_details_extractor":                       &compute.HostDetailsExtractor{},

	"netapp_storage_pool_cpu_usage_extractor": &storage.StoragePoolCPUUsageExtractor{},
}
