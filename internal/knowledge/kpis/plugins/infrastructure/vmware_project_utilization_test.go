// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package infrastructure

import (
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/identity"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	testlibDB "github.com/cobaltcore-dev/cortex/internal/knowledge/db/testing"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	prometheusgo "github.com/prometheus/client_model/go"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func buildMetricKey(name string, labels map[string]string) string {
	switch name {
	case "cortex_vmware_project_instances":
		return name + "|" + labels["compute_host"] + "|" + labels["project_id"] +
			"|" + labels["flavor_name"] + "|" + labels["availability_zone"]
	case "cortex_vmware_project_capacity_usage":
		return name + "|" + labels["compute_host"] + "|" + labels["project_id"] +
			"|" + labels["availability_zone"] + "|" + labels["resource"]
	default:
		return name
	}
}

func instanceMetric(computeHost, az, projectID, projectName, flavorName string, value float64) collectedVMwareMetric {
	labels := mockVMwareHostLabels(computeHost, az)
	labels["project_id"] = projectID
	labels["project_name"] = projectName
	labels["flavor_name"] = flavorName
	return collectedVMwareMetric{Name: "cortex_vmware_project_instances", Labels: labels, Value: value}
}

func capacityMetric(computeHost, az, projectID, projectName, resource string, value float64) collectedVMwareMetric {
	labels := mockVMwareHostLabels(computeHost, az)
	labels["project_id"] = projectID
	labels["project_name"] = projectName
	labels["resource"] = resource
	return collectedVMwareMetric{Name: "cortex_vmware_project_capacity_usage", Labels: labels, Value: value}
}

func buildVMwareHostDetailsClient(t *testing.T, hostDetails []compute.HostDetails) *fake.ClientBuilder {
	t.Helper()
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("failed to build scheme: %v", err)
	}
	raw, err := v1alpha1.BoxFeatureList(hostDetails)
	if err != nil {
		t.Fatalf("failed to box host details: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(
		&v1alpha1.Knowledge{
			ObjectMeta: v1.ObjectMeta{Name: "host-details"},
			Status:     v1alpha1.KnowledgeStatus{Raw: raw},
		},
	)
}

func TestVMwareProjectUtilizationKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	kpi := &VMwareProjectUtilizationKPI{}
	if err := kpi.Init(&testDB, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestVMwareProjectUtilizationKPI_getVMwareHosts(t *testing.T) {
	hostDetails := []compute.HostDetails{
		{
			ComputeHost:      "nova-compute-1",
			HypervisorFamily: hypervisorFamilyVMware,
		},
		{
			ComputeHost:      "nova-compute-2",
			HypervisorFamily: hypervisorFamilyVMware,
		},
		{
			ComputeHost:      "nova-compute-ironic-1",
			HypervisorType:   vmwareIronicHypervisorType,
			HypervisorFamily: hypervisorFamilyVMware,
		},
		{
			ComputeHost:      "nova-compute-3",
			HypervisorFamily: "other",
		},
	}

	clientBuilder := buildVMwareHostDetailsClient(t, hostDetails)
	kpi := &VMwareProjectUtilizationKPI{}
	kpi.Client = clientBuilder.Build()

	hostMapping, err := kpi.getVMwareHosts()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedHosts := map[string]vmwareHost{
		"nova-compute-1": {HostDetails: hostDetails[0]},
		"nova-compute-2": {HostDetails: hostDetails[1]},
	}

	if len(hostMapping) != len(expectedHosts) {
		t.Fatalf("expected %d hosts, got %d", len(expectedHosts), len(hostMapping))
	}

	for computeHost, expectedHost := range expectedHosts {
		host, ok := hostMapping[computeHost]
		if !ok {
			t.Fatalf("expected host %s not found in mapping", computeHost)
		}
		if host.ComputeHost != expectedHost.ComputeHost || host.HypervisorFamily != expectedHost.HypervisorFamily {
			t.Errorf("host details mismatch for %s: expected %+v, got %+v", computeHost, expectedHost, host)
		}
	}
}

func TestVMwareProjectUtilizationKPI_queryProjectInstanceCount(t *testing.T) {
	tests := []struct {
		name           string
		servers        []nova.Server
		projects       []identity.Project
		expectedCounts map[string]vmwareProjectInstanceCount
	}{
		{
			name: "single instance in one project",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One"}},
			expectedCounts: map[string]vmwareProjectInstanceCount{
				"project-1|nova-compute-1|flavor-1|az1": {ProjectID: "project-1", ProjectName: "Project One", ComputeHost: "nova-compute-1", FlavorName: "flavor-1", AvailabilityZone: "az1", InstanceCount: 1},
			},
		},
		{
			name: "multiple instances across projects and hosts",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "server-2", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-2", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "server-3", TenantID: "project-2", OSEXTSRVATTRHost: "nova-compute-2", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az2"},
				{ID: "server-4", TenantID: "project-2", OSEXTSRVATTRHost: "nova-compute-2", FlavorName: "flavor-2", Status: "ACTIVE", OSEXTAvailabilityZone: "az2"},
			},
			projects: []identity.Project{
				{ID: "project-1", Name: "Project One"},
				{ID: "project-2", Name: "Project Two"},
			},
			expectedCounts: map[string]vmwareProjectInstanceCount{
				"project-1|nova-compute-1|flavor-1|az1": {ProjectID: "project-1", ProjectName: "Project One", ComputeHost: "nova-compute-1", FlavorName: "flavor-1", AvailabilityZone: "az1", InstanceCount: 1},
				"project-1|nova-compute-1|flavor-2|az1": {ProjectID: "project-1", ProjectName: "Project One", ComputeHost: "nova-compute-1", FlavorName: "flavor-2", AvailabilityZone: "az1", InstanceCount: 1},
				"project-2|nova-compute-2|flavor-1|az2": {ProjectID: "project-2", ProjectName: "Project Two", ComputeHost: "nova-compute-2", FlavorName: "flavor-1", AvailabilityZone: "az2", InstanceCount: 1},
				"project-2|nova-compute-2|flavor-2|az2": {ProjectID: "project-2", ProjectName: "Project Two", ComputeHost: "nova-compute-2", FlavorName: "flavor-2", AvailabilityZone: "az2", InstanceCount: 1},
			},
		},
		{
			name: "instances on non-VMware hosts are excluded",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "server-2", TenantID: "project-1", OSEXTSRVATTRHost: "node-3", FlavorName: "flavor-2", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "server-3", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-ironic-1", FlavorName: "flavor-2", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One"}},
			expectedCounts: map[string]vmwareProjectInstanceCount{
				"project-1|nova-compute-1|flavor-1|az1": {ProjectID: "project-1", ProjectName: "Project One", ComputeHost: "nova-compute-1", FlavorName: "flavor-1", AvailabilityZone: "az1", InstanceCount: 1},
			},
		},
		{
			name: "instances with non-ACTIVE status are excluded",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-1", Status: "DELETED", OSEXTAvailabilityZone: "az1"},
				{ID: "server-2", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-2", Status: "ERROR", OSEXTAvailabilityZone: "az1"},
				{ID: "server-3", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-3", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One"}},
			expectedCounts: map[string]vmwareProjectInstanceCount{
				"project-1|nova-compute-1|flavor-3|az1": {ProjectID: "project-1", ProjectName: "Project One", ComputeHost: "nova-compute-1", FlavorName: "flavor-3", AvailabilityZone: "az1", InstanceCount: 1},
			},
		},
		{
			name: "multiple instances with same key are counted correctly",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "server-2", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "server-3", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-2", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az2"},
				{ID: "server-4", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-2", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az2"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One"}},
			expectedCounts: map[string]vmwareProjectInstanceCount{
				"project-1|nova-compute-1|flavor-1|az1": {ProjectID: "project-1", ProjectName: "Project One", ComputeHost: "nova-compute-1", FlavorName: "flavor-1", AvailabilityZone: "az1", InstanceCount: 2},
				"project-1|nova-compute-2|flavor-1|az2": {ProjectID: "project-1", ProjectName: "Project One", ComputeHost: "nova-compute-2", FlavorName: "flavor-1", AvailabilityZone: "az2", InstanceCount: 2},
			},
		},
		{
			name: "missing project entry results in empty project_name",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{},
			expectedCounts: map[string]vmwareProjectInstanceCount{
				"project-1|nova-compute-1|flavor-1|az1": {ProjectID: "project-1", ProjectName: "", ComputeHost: "nova-compute-1", FlavorName: "flavor-1", AvailabilityZone: "az1", InstanceCount: 1},
			},
		},
		{
			name:           "no instances returns empty result",
			servers:        []nova.Server{},
			projects:       []identity.Project{{ID: "project-1", Name: "Project One"}},
			expectedCounts: map[string]vmwareProjectInstanceCount{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbEnv := testlibDB.SetupDBEnv(t)
			testDB := db.DB{DbMap: dbEnv.DbMap}
			defer dbEnv.Close()

			if err := testDB.CreateTable(
				testDB.AddTable(nova.Server{}),
				testDB.AddTable(identity.Project{}),
			); err != nil {
				t.Fatalf("failed to create tables: %v", err)
			}

			var mockData []any
			for i := range tt.servers {
				mockData = append(mockData, &tt.servers[i])
			}
			for i := range tt.projects {
				mockData = append(mockData, &tt.projects[i])
			}
			if len(mockData) > 0 {
				if err := testDB.Insert(mockData...); err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			}

			client := buildVMwareHostDetailsClient(t, []compute.HostDetails{})
			kpi := &VMwareProjectUtilizationKPI{}
			if err := kpi.Init(&testDB, client.Build(), conf.NewRawOpts("{}")); err != nil {
				t.Fatalf("expected no error on Init, got %v", err)
			}
			counts, err := kpi.queryProjectInstanceCount()
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if len(counts) != len(tt.expectedCounts) {
				t.Fatalf("expected %d counts, got %d", len(tt.expectedCounts), len(counts))
			}
			for _, got := range counts {
				key := got.ProjectID + "|" + got.ComputeHost + "|" + got.FlavorName + "|" + got.AvailabilityZone
				exp, ok := tt.expectedCounts[key]
				if !ok {
					t.Errorf("unexpected count for key %q: %+v", key, got)
					continue
				}
				if got != exp {
					t.Errorf("count mismatch for key %q: expected %+v, got %+v", key, exp, got)
				}
			}
		})
	}
}

func TestVMwareProjectUtilizationKPI_queryProjectCapacityUsage(t *testing.T) {
	tests := []struct {
		name           string
		servers        []nova.Server
		projects       []identity.Project
		flavors        []nova.Flavor
		expectedUsages map[string]vmwareProjectCapacityUsage
	}{
		{
			name: "single instance with flavor details",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One"}},
			flavors:  []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			expectedUsages: map[string]vmwareProjectCapacityUsage{
				"project-1|nova-compute-1|az1": {ProjectID: "project-1", ProjectName: "Project One", ComputeHost: "nova-compute-1", AvailabilityZone: "az1", TotalVCPUs: 2, TotalRAMMB: 4096, TotalDiskGB: 1},
			},
		},
		{
			name: "multiple instances with different flavors and projects",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "server-2", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-2", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "server-3", TenantID: "project-2", OSEXTSRVATTRHost: "nova-compute-2", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az2"},
			},
			projects: []identity.Project{
				{ID: "project-1", Name: "Project One"},
				{ID: "project-2", Name: "Project Two"},
			},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1},
				{ID: "f2", Name: "flavor-2", VCPUs: 4, RAM: 8192, Disk: 2},
			},
			expectedUsages: map[string]vmwareProjectCapacityUsage{
				"project-1|nova-compute-1|az1": {ProjectID: "project-1", ProjectName: "Project One", ComputeHost: "nova-compute-1", AvailabilityZone: "az1", TotalVCPUs: 6, TotalRAMMB: 12288, TotalDiskGB: 3},
				"project-2|nova-compute-2|az2": {ProjectID: "project-2", ProjectName: "Project Two", ComputeHost: "nova-compute-2", AvailabilityZone: "az2", TotalVCPUs: 2, TotalRAMMB: 4096, TotalDiskGB: 1},
			},
		},
		{
			name: "missing flavor entry results in zero capacity",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-missing", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One"}},
			flavors:  []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			expectedUsages: map[string]vmwareProjectCapacityUsage{
				"project-1|nova-compute-1|az1": {ProjectID: "project-1", ProjectName: "Project One", ComputeHost: "nova-compute-1", AvailabilityZone: "az1", TotalVCPUs: 0, TotalRAMMB: 0, TotalDiskGB: 0},
			},
		},
		{
			name: "instances on non-VMware hosts are excluded",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "node-3", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects:       []identity.Project{{ID: "project-1", Name: "Project One"}},
			flavors:        []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			expectedUsages: map[string]vmwareProjectCapacityUsage{},
		},
		{
			name: "instances with non-ACTIVE status are excluded",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-1", Status: "DELETED", OSEXTAvailabilityZone: "az1"},
			},
			projects:       []identity.Project{{ID: "project-1", Name: "Project One"}},
			flavors:        []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			expectedUsages: map[string]vmwareProjectCapacityUsage{},
		},
		{
			name:    "no instances returns empty capacity usage",
			servers: []nova.Server{},
			projects: []identity.Project{
				{ID: "project-1", Name: "Project One"},
			},
			flavors:        []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			expectedUsages: map[string]vmwareProjectCapacityUsage{},
		},
		{
			name: "multiple instances with same flavor aggregate capacity correctly",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "server-2", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One"}},
			flavors:  []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			expectedUsages: map[string]vmwareProjectCapacityUsage{
				"project-1|nova-compute-1|az1": {ProjectID: "project-1", ProjectName: "Project One", ComputeHost: "nova-compute-1", AvailabilityZone: "az1", TotalVCPUs: 4, TotalRAMMB: 8192, TotalDiskGB: 2},
			},
		},
		{
			name: "ironic host instances are excluded",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-ironic-1", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects:       []identity.Project{{ID: "project-1", Name: "Project One"}},
			flavors:        []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			expectedUsages: map[string]vmwareProjectCapacityUsage{},
		},
		{
			name: "missing project entry results in empty project_name",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{},
			flavors:  []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			expectedUsages: map[string]vmwareProjectCapacityUsage{
				"project-1|nova-compute-1|az1": {ProjectID: "project-1", ProjectName: "", ComputeHost: "nova-compute-1", AvailabilityZone: "az1", TotalVCPUs: 2, TotalRAMMB: 4096, TotalDiskGB: 1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbEnv := testlibDB.SetupDBEnv(t)
			testDB := db.DB{DbMap: dbEnv.DbMap}
			defer dbEnv.Close()

			if err := testDB.CreateTable(
				testDB.AddTable(nova.Server{}),
				testDB.AddTable(identity.Project{}),
				testDB.AddTable(nova.Flavor{}),
			); err != nil {
				t.Fatalf("failed to create tables: %v", err)
			}

			var mockData []any
			for i := range tt.servers {
				mockData = append(mockData, &tt.servers[i])
			}
			for i := range tt.projects {
				mockData = append(mockData, &tt.projects[i])
			}
			for i := range tt.flavors {
				mockData = append(mockData, &tt.flavors[i])
			}
			if len(mockData) > 0 {
				if err := testDB.Insert(mockData...); err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			}

			client := buildVMwareHostDetailsClient(t, []compute.HostDetails{})
			kpi := &VMwareProjectUtilizationKPI{}
			if err := kpi.Init(&testDB, client.Build(), conf.NewRawOpts("{}")); err != nil {
				t.Fatalf("expected no error on Init, got %v", err)
			}
			usages, err := kpi.queryProjectCapacityUsage()
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if len(usages) != len(tt.expectedUsages) {
				t.Fatalf("expected %d usages, got %d", len(tt.expectedUsages), len(usages))
			}
			for _, got := range usages {
				key := got.ProjectID + "|" + got.ComputeHost + "|" + got.AvailabilityZone
				exp, ok := tt.expectedUsages[key]
				if !ok {
					t.Errorf("unexpected usage for key %q: %+v", key, got)
					continue
				}
				if got != exp {
					t.Errorf("usage mismatch for key %q: expected %+v, got %+v", key, exp, got)
				}
			}
		})
	}
}

func TestVMwareProjectUtilizationKPI_Collect(t *testing.T) {
	tests := []struct {
		name            string
		servers         []nova.Server
		projects        []identity.Project
		flavors         []nova.Flavor
		hostDetails     []compute.HostDetails
		expectedMetrics []collectedVMwareMetric
	}{
		{
			name: "single instance in one project",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One"}},
			flavors:  []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			hostDetails: []compute.HostDetails{
				{ComputeHost: "nova-compute-1", HypervisorFamily: hypervisorFamilyVMware, AvailabilityZone: "az1"},
			},
			expectedMetrics: []collectedVMwareMetric{
				instanceMetric("nova-compute-1", "az1", "project-1", "Project One", "flavor-1", 1),
				capacityMetric("nova-compute-1", "az1", "project-1", "Project One", "vcpu", 2),
				capacityMetric("nova-compute-1", "az1", "project-1", "Project One", "memory", 4096*1024*1024),
				capacityMetric("nova-compute-1", "az1", "project-1", "Project One", "disk", 1*1024*1024*1024),
			},
		},
		{
			name: "multiple instances across hosts, projects, and flavors",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "s2", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-2", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "s3", TenantID: "project-2", OSEXTSRVATTRHost: "nova-compute-2", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az2"},
			},
			projects: []identity.Project{
				{ID: "project-1", Name: "Project One"},
				{ID: "project-2", Name: "Project Two"},
			},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1},
				{ID: "f2", Name: "flavor-2", VCPUs: 4, RAM: 8192, Disk: 2},
			},
			hostDetails: []compute.HostDetails{
				{ComputeHost: "nova-compute-1", HypervisorFamily: hypervisorFamilyVMware, AvailabilityZone: "az1"},
				{ComputeHost: "nova-compute-2", HypervisorFamily: hypervisorFamilyVMware, AvailabilityZone: "az2"},
			},
			expectedMetrics: []collectedVMwareMetric{
				instanceMetric("nova-compute-1", "az1", "project-1", "Project One", "flavor-1", 1),
				instanceMetric("nova-compute-1", "az1", "project-1", "Project One", "flavor-2", 1),
				instanceMetric("nova-compute-2", "az2", "project-2", "Project Two", "flavor-1", 1),
				// nova-compute-1/project-1: 1*flavor-1 + 1*flavor-2
				capacityMetric("nova-compute-1", "az1", "project-1", "Project One", "vcpu", 6),
				capacityMetric("nova-compute-1", "az1", "project-1", "Project One", "memory", 12288*1024*1024),
				capacityMetric("nova-compute-1", "az1", "project-1", "Project One", "disk", 3*1024*1024*1024),
				// nova-compute-2/project-2: 1*flavor-1
				capacityMetric("nova-compute-2", "az2", "project-2", "Project Two", "vcpu", 2),
				capacityMetric("nova-compute-2", "az2", "project-2", "Project Two", "memory", 4096*1024*1024),
				capacityMetric("nova-compute-2", "az2", "project-2", "Project Two", "disk", 1*1024*1024*1024),
			},
		},
		{
			name: "non-VMware and ironic hosts are excluded",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "s2", TenantID: "project-1", OSEXTSRVATTRHost: "node-3", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "s3", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-ironic-1", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One"}},
			flavors:  []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			hostDetails: []compute.HostDetails{
				{ComputeHost: "nova-compute-1", HypervisorFamily: hypervisorFamilyVMware, AvailabilityZone: "az1"},
			},
			expectedMetrics: []collectedVMwareMetric{
				instanceMetric("nova-compute-1", "az1", "project-1", "Project One", "flavor-1", 1),
				capacityMetric("nova-compute-1", "az1", "project-1", "Project One", "vcpu", 2),
				capacityMetric("nova-compute-1", "az1", "project-1", "Project One", "memory", 4096*1024*1024),
				capacityMetric("nova-compute-1", "az1", "project-1", "Project One", "disk", 1*1024*1024*1024),
			},
		},
		{
			name: "DELETED and ERROR instances are excluded",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-1", Status: "DELETED", OSEXTAvailabilityZone: "az1"},
				{ID: "s2", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-2", Status: "ERROR", OSEXTAvailabilityZone: "az1"},
				{ID: "s3", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-3", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One"}},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1},
				{ID: "f2", Name: "flavor-2", VCPUs: 4, RAM: 8192, Disk: 2},
				{ID: "f3", Name: "flavor-3", VCPUs: 8, RAM: 16384, Disk: 4},
			},
			hostDetails: []compute.HostDetails{
				{ComputeHost: "nova-compute-1", HypervisorFamily: hypervisorFamilyVMware, AvailabilityZone: "az1"},
			},
			expectedMetrics: []collectedVMwareMetric{
				instanceMetric("nova-compute-1", "az1", "project-1", "Project One", "flavor-3", 1),
				capacityMetric("nova-compute-1", "az1", "project-1", "Project One", "vcpu", 8),
				capacityMetric("nova-compute-1", "az1", "project-1", "Project One", "memory", 16384*1024*1024),
				capacityMetric("nova-compute-1", "az1", "project-1", "Project One", "disk", 4*1024*1024*1024),
			},
		},
		{
			name: "multiple instances with same flavor are aggregated correctly",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "s2", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "s3", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-2", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az2"},
				{ID: "s4", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-2", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az2"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One"}},
			flavors:  []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			hostDetails: []compute.HostDetails{
				{ComputeHost: "nova-compute-1", HypervisorFamily: hypervisorFamilyVMware, AvailabilityZone: "az1"},
				{ComputeHost: "nova-compute-2", HypervisorFamily: hypervisorFamilyVMware, AvailabilityZone: "az2"},
			},
			expectedMetrics: []collectedVMwareMetric{
				instanceMetric("nova-compute-1", "az1", "project-1", "Project One", "flavor-1", 2),
				instanceMetric("nova-compute-2", "az2", "project-1", "Project One", "flavor-1", 2),
				capacityMetric("nova-compute-1", "az1", "project-1", "Project One", "vcpu", 4),
				capacityMetric("nova-compute-1", "az1", "project-1", "Project One", "memory", 2*4096*1024*1024),
				capacityMetric("nova-compute-1", "az1", "project-1", "Project One", "disk", 2*1024*1024*1024),
				capacityMetric("nova-compute-2", "az2", "project-1", "Project One", "vcpu", 4),
				capacityMetric("nova-compute-2", "az2", "project-1", "Project One", "memory", 2*4096*1024*1024),
				capacityMetric("nova-compute-2", "az2", "project-1", "Project One", "disk", 2*1024*1024*1024),
			},
		},
		{
			name: "missing project entry results in empty project_name label",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{},
			flavors:  []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			hostDetails: []compute.HostDetails{
				{ComputeHost: "nova-compute-1", HypervisorFamily: hypervisorFamilyVMware, AvailabilityZone: "az1"},
			},
			expectedMetrics: []collectedVMwareMetric{
				instanceMetric("nova-compute-1", "az1", "project-1", "", "flavor-1", 1),
				capacityMetric("nova-compute-1", "az1", "project-1", "", "vcpu", 2),
				capacityMetric("nova-compute-1", "az1", "project-1", "", "memory", 4096*1024*1024),
				capacityMetric("nova-compute-1", "az1", "project-1", "", "disk", 1*1024*1024*1024),
			},
		},
		{
			name: "missing flavor entry results in zero capacity",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-missing", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One"}},
			flavors:  []nova.Flavor{},
			hostDetails: []compute.HostDetails{
				{ComputeHost: "nova-compute-1", HypervisorFamily: hypervisorFamilyVMware, AvailabilityZone: "az1"},
			},
			expectedMetrics: []collectedVMwareMetric{
				instanceMetric("nova-compute-1", "az1", "project-1", "Project One", "flavor-missing", 1),
				capacityMetric("nova-compute-1", "az1", "project-1", "Project One", "vcpu", 0),
				capacityMetric("nova-compute-1", "az1", "project-1", "Project One", "memory", 0),
				capacityMetric("nova-compute-1", "az1", "project-1", "Project One", "disk", 0),
			},
		},
		{
			name:    "no instances produces no metrics",
			servers: []nova.Server{},
			projects: []identity.Project{
				{ID: "project-1", Name: "Project One"},
			},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1},
			},
			hostDetails: []compute.HostDetails{
				{ComputeHost: "nova-compute-1", HypervisorFamily: hypervisorFamilyVMware, AvailabilityZone: "az1"},
			},
			expectedMetrics: []collectedVMwareMetric{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbEnv := testlibDB.SetupDBEnv(t)
			testDB := db.DB{DbMap: dbEnv.DbMap}
			defer dbEnv.Close()

			if err := testDB.CreateTable(
				testDB.AddTable(nova.Server{}),
				testDB.AddTable(identity.Project{}),
				testDB.AddTable(nova.Flavor{}),
			); err != nil {
				t.Fatalf("failed to create tables: %v", err)
			}

			var mockData []any
			for i := range tt.servers {
				mockData = append(mockData, &tt.servers[i])
			}
			for i := range tt.projects {
				mockData = append(mockData, &tt.projects[i])
			}
			for i := range tt.flavors {
				mockData = append(mockData, &tt.flavors[i])
			}
			if len(mockData) > 0 {
				if err := testDB.Insert(mockData...); err != nil {
					t.Fatalf("expected no error inserting data, got %v", err)
				}
			}

			client := buildVMwareHostDetailsClient(t, tt.hostDetails)
			kpi := &VMwareProjectUtilizationKPI{}
			if err := kpi.Init(&testDB, client.Build(), conf.NewRawOpts("{}")); err != nil {
				t.Fatalf("expected no error on Init, got %v", err)
			}

			ch := make(chan prometheus.Metric, 100)
			kpi.Collect(ch)
			close(ch)

			actual := make(map[string]collectedVMwareMetric)
			for m := range ch {
				var pm prometheusgo.Metric
				if err := m.Write(&pm); err != nil {
					t.Fatalf("failed to write metric: %v", err)
				}
				labels := make(map[string]string)
				for _, lbl := range pm.Label {
					labels[lbl.GetName()] = lbl.GetValue()
				}
				name := getMetricName(m.Desc().String())
				key := buildMetricKey(name, labels)
				if _, exists := actual[key]; exists {
					t.Fatalf("duplicate metric key %q", key)
				}
				actual[key] = collectedVMwareMetric{Name: name, Labels: labels, Value: pm.GetGauge().GetValue()}
			}

			if len(actual) != len(tt.expectedMetrics) {
				t.Errorf("expected %d metrics, got %d: actual=%v", len(tt.expectedMetrics), len(actual), actual)
			}
			for _, exp := range tt.expectedMetrics {
				key := buildMetricKey(exp.Name, exp.Labels)
				got, ok := actual[key]
				if !ok {
					t.Errorf("missing metric %q", key)
					continue
				}
				if got.Value != exp.Value {
					t.Errorf("metric %q value: expected %v, got %v", key, exp.Value, got.Value)
				}
				if !reflect.DeepEqual(exp.Labels, got.Labels) {
					t.Errorf("metric %q labels: expected %v, got %v", key, exp.Labels, got.Labels)
				}
			}
		})
	}
}
