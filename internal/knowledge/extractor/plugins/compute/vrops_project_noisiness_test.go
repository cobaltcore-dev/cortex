// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"slices"
	"testing"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/prometheus"
)

func TestVROpsProjectNoisinessExtractor_Extract(t *testing.T) {
	in := VROpsProjectNoisinessInputs{
		Metrics: []prometheus.VROpsVMMetric{
			{Project: "project1", Value: 50, InstanceUUID: "uuid1"},
			{Project: "project1", Value: 60, InstanceUUID: "uuid2"},
			{Project: "project2", Value: 70, InstanceUUID: "uuid3"},
		},
		Servers: []nova.Server{
			{ID: "uuid1", TenantID: "project1", OSEXTSRVATTRHypervisorHostname: "host1"},
			{ID: "uuid2", TenantID: "project1", OSEXTSRVATTRHypervisorHostname: "host2"},
			{ID: "uuid3", TenantID: "project2", OSEXTSRVATTRHypervisorHostname: "host1"},
			// Servers without metrics are skipped.
			{ID: "uuid4", TenantID: "project3", OSEXTSRVATTRHypervisorHostname: "host2"},
		},
		Hypervisors: []nova.Hypervisor{
			{ID: "1", Hostname: "host1", ServiceHost: "service_host1"},
			{ID: "2", Hostname: "host2", ServiceHost: "service_host2"},
		},
	}
	features, err := (&VROpsProjectNoisinessExtractor{}).Extract(t.Context(), in)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	expected := []VROpsProjectNoisiness{
		{Project: "project1", ComputeHost: "service_host1", AvgCPUOfProject: 55},
		{Project: "project1", ComputeHost: "service_host2", AvgCPUOfProject: 55},
		{Project: "project2", ComputeHost: "service_host1", AvgCPUOfProject: 70},
	}
	if !slices.Equal(features, expected) {
		t.Errorf("expected %+v, got %+v", expected, features)
	}
}
