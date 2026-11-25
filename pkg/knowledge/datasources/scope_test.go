// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package datasources_test

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/placement"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/prometheus"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store"
)

func TestScopeFor(t *testing.T) {
	tests := []struct {
		name    string
		spec    v1alpha1.DatasourceSpec
		want    store.Scope
		wantErr bool
	}{
		{
			name: "nova hypervisors",
			spec: v1alpha1.DatasourceSpec{Type: v1alpha1.DatasourceTypeOpenStack, OpenStack: v1alpha1.OpenStackDatasource{
				Type: v1alpha1.OpenStackDatasourceTypeNova,
				Nova: v1alpha1.NovaDatasource{Type: v1alpha1.NovaDatasourceTypeHypervisors},
			}},
			want: store.Scope{Model: nova.Hypervisor{}},
		},
		{
			name: "placement traits",
			spec: v1alpha1.DatasourceSpec{Type: v1alpha1.DatasourceTypeOpenStack, OpenStack: v1alpha1.OpenStackDatasource{
				Type:      v1alpha1.OpenStackDatasourceTypePlacement,
				Placement: v1alpha1.PlacementDatasource{Type: v1alpha1.PlacementDatasourceTypeResourceProviderTraits},
			}},
			want: store.Scope{Model: placement.Trait{}},
		},
		{
			name: "prometheus metric partitioned by alias",
			spec: v1alpha1.DatasourceSpec{Type: v1alpha1.DatasourceTypePrometheus, Prometheus: v1alpha1.PrometheusDatasource{
				Type: "vrops_vm_metric", Alias: "vrops_virtualmachine_cpu_demand_ratio",
			}},
			want: store.Scope{Model: prometheus.VROpsVMMetric{}, Partition: "vrops_virtualmachine_cpu_demand_ratio"},
		},
		{
			name: "unknown nova type",
			spec: v1alpha1.DatasourceSpec{Type: v1alpha1.DatasourceTypeOpenStack, OpenStack: v1alpha1.OpenStackDatasource{
				Type: v1alpha1.OpenStackDatasourceTypeNova,
				Nova: v1alpha1.NovaDatasource{Type: "unknown"},
			}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := datasources.ScopeFor(tt.spec)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("expected %+v, got %+v", tt.want, got)
			}
		})
	}
}
