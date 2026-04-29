// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"net/http"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
)

// All supported Prometheus metric syncers.
var supportedMetricSyncers = map[string]func(
	ds v1alpha1.Datasource,
	authenticatedDB *db.DB,
	authenticatedHTTP *http.Client,
	prometheusURL string,
	monitor datasources.Monitor,
) typedSyncer{
	"vrops_host_metric":                     newTypedSyncer[VROpsHostMetric],
	"vrops_vm_metric":                       newTypedSyncer[VROpsVMMetric],
	"node_exporter_metric":                  newTypedSyncer[NodeExporterMetric],
	"netapp_aggregate_labels_metric":        newTypedSyncer[NetAppAggregateLabelsMetric],
	"netapp_node_metric":                    newTypedSyncer[NetAppNodeMetric],
	"netapp_volume_aggregate_labels_metric": newTypedSyncer[NetAppVolumeAggrLabelsMetric],
	"kvm_libvirt_domain_metric":             newTypedSyncer[KVMDomainMetric],
	"generic":                               newTypedSyncer[GenericMetric],
}
