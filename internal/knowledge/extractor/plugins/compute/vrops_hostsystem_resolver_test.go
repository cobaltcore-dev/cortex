// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"slices"
	"testing"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/prometheus"
)

func TestVROpsHostsystemResolver_Extract(t *testing.T) {
	in := VROpsHostsystemResolverInputs{
		Metrics: []prometheus.VROpsVMMetric{
			{HostSystem: "hostsystem2", InstanceUUID: "uuid2"},
			{HostSystem: "hostsystem1", InstanceUUID: "uuid1"},
			// Duplicate metrics of the same server are deduplicated.
			{HostSystem: "hostsystem1", InstanceUUID: "uuid1"},
			// Metrics of unknown servers are skipped.
			{HostSystem: "hostsystem3", InstanceUUID: "unknown"},
			// Servers without host are skipped.
			{HostSystem: "hostsystem4", InstanceUUID: "uuid4"},
		},
		Servers: []nova.Server{
			{ID: "uuid1", OSEXTSRVATTRHost: "service_host1"},
			{ID: "uuid2", OSEXTSRVATTRHost: "service_host2"},
			{ID: "uuid4", OSEXTSRVATTRHost: ""},
		},
	}
	features, err := (&VROpsHostsystemResolver{}).Extract(t.Context(), in)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	expected := []ResolvedVROpsHostsystem{
		{VROpsHostsystem: "hostsystem1", NovaComputeHost: "service_host1"},
		{VROpsHostsystem: "hostsystem2", NovaComputeHost: "service_host2"},
	}
	if !slices.Equal(features, expected) {
		t.Errorf("expected %+v, got %+v", expected, features)
	}
}
