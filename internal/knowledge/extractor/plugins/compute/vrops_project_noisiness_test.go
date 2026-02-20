// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/prometheus"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	testlibDB "github.com/cobaltcore-dev/cortex/internal/knowledge/db/testing"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestVROpsProjectNoisinessExtractor_Init(t *testing.T) {
	extractor := &VROpsProjectNoisinessExtractor{}

	config := v1alpha1.KnowledgeSpec{
		Extractor: v1alpha1.KnowledgeExtractorSpec{
			Name:   "vrops_project_noisiness_extractor",
			Config: runtime.RawExtension{Raw: []byte(`{}`)},
		},
	}
	if err := extractor.Init(nil, nil, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestVROpsProjectNoisinessExtractor_Extract(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(prometheus.VROpsVMMetric{}),
		testDB.AddTable(nova.Server{}),
		testDB.AddTable(nova.Hypervisor{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	vropsVMMetrics := []any{
		&prometheus.VROpsVMMetric{Name: "vrops_virtualmachine_cpu_demand_ratio", Project: "project1", Value: 50, InstanceUUID: "uuid1"},
		&prometheus.VROpsVMMetric{Name: "vrops_virtualmachine_cpu_demand_ratio", Project: "project1", Value: 60, InstanceUUID: "uuid2"},
		&prometheus.VROpsVMMetric{Name: "vrops_virtualmachine_cpu_demand_ratio", Project: "project2", Value: 70, InstanceUUID: "uuid3"},
	}
	if err := testDB.Insert(vropsVMMetrics...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	servers := []any{
		&nova.Server{ID: "uuid1", TenantID: "project1", OSEXTSRVATTRHypervisorHostname: "host1"},
		&nova.Server{ID: "uuid2", TenantID: "project1", OSEXTSRVATTRHypervisorHostname: "host2"},
		&nova.Server{ID: "uuid3", TenantID: "project2", OSEXTSRVATTRHypervisorHostname: "host1"},
	}
	if err := testDB.Insert(servers...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hypervisors := []any{
		&nova.Hypervisor{ID: "1", Hostname: "host1", ServiceHost: "service_host1"},
		&nova.Hypervisor{ID: "2", Hostname: "host2", ServiceHost: "service_host2"},
	}
	if err := testDB.Insert(hypervisors...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	extractor := &VROpsProjectNoisinessExtractor{}

	config := v1alpha1.KnowledgeSpec{
		Extractor: v1alpha1.KnowledgeExtractorSpec{
			Name:   "vrops_project_noisiness_extractor",
			Config: runtime.RawExtension{Raw: []byte(`{}`)},
		},
	}

	if err := extractor.Init(&testDB, nil, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	features, err := extractor.Extract([]*v1alpha1.Datasource{}, []*v1alpha1.Knowledge{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expected := []VROpsProjectNoisiness{
		{Project: "project1", ComputeHost: "service_host1", AvgCPUOfProject: 55},
		{Project: "project1", ComputeHost: "service_host2", AvgCPUOfProject: 55},
		{Project: "project2", ComputeHost: "service_host1", AvgCPUOfProject: 70},
	}
	if len(features) != len(expected) {
		t.Fatalf("expected %d rows, got %d", len(expected), len(features))
	}
	for i, f := range features {
		n := f.(VROpsProjectNoisiness)
		// Find the expected row
		found := false
		for _, e := range expected {
			if n.Project == e.Project && n.ComputeHost == e.ComputeHost && n.AvgCPUOfProject == e.AvgCPUOfProject {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("unexpected row at index %d: %+v", i, n)
		}
	}
}
