// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/kvm"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/netapp"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/sap"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/vmware"
)

// All supported feature extractors.
var supportedExtractors = map[string]plugins.FeatureExtractor{
	// VMware-specific extractors
	"vrops_hostsystem_resolver":                        &vmware.VROpsHostsystemResolver{},
	"vrops_project_noisiness_extractor":                &vmware.VROpsProjectNoisinessExtractor{},
	"vrops_hostsystem_contention_long_term_extractor":  &vmware.VROpsHostsystemContentionLongTermExtractor{},
	"vrops_hostsystem_contention_short_term_extractor": &vmware.VROpsHostsystemContentionShortTermExtractor{},
	// KVM-specific extractors
	"kvm_libvirt_domain_cpu_steal_pct_extractor": &kvm.LibvirtDomainCPUStealPctExtractor{},
	// NetApp-specific extractors
	"netapp_storage_pool_cpu_usage_extractor": &netapp.StoragePoolCPUUsageExtractor{},
	// Shared extractors
	"host_utilization_extractor":       &shared.HostUtilizationExtractor{},
	"host_capabilities_extractor":      &shared.HostCapabilitiesExtractor{},
	"vm_host_residency_extractor":      &shared.VMHostResidencyExtractor{},
	"vm_life_span_histogram_extractor": &shared.VMLifeSpanHistogramExtractor{},
	"host_az_extractor":                &shared.HostAZExtractor{},
	"host_pinned_projects_extractor":   &shared.HostPinnedProjectsExtractor{},
	// SAP-specific extractors
	"sap_host_details_extractor": &sap.HostDetailsExtractor{},
}
