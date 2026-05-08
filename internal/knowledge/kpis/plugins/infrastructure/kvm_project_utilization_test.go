// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package infrastructure

import (
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/identity"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	testlibDB "github.com/cobaltcore-dev/cortex/internal/knowledge/db/testing"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	prometheusgo "github.com/prometheus/client_model/go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type collectedKVMMetric struct {
	Name   string
	Labels map[string]string
	Value  float64
}

func buildKVMMetricKey(name string, labels map[string]string) string {
	switch name {
	case "cortex_kvm_project_instances":
		return name + "|" + labels["compute_host"] + "|" + labels["project_id"] +
			"|" + labels["flavor_name"] + "|" + labels["availability_zone"]
	case "cortex_kvm_project_capacity_usage":
		return name + "|" + labels["compute_host"] + "|" + labels["project_id"] +
			"|" + labels["availability_zone"] + "|" + labels["resource"]
	default:
		return name
	}
}

func kvmInstanceMetric(computeHost, az, projectID, projectName, domainID, domainName, flavorName string, value float64) collectedKVMMetric {
	labels := mockKVMHostLabels(computeHost, az)
	labels["project_id"] = projectID
	labels["project_name"] = projectName
	labels["domain_id"] = domainID
	labels["domain_name"] = domainName
	labels["flavor_name"] = flavorName
	return collectedKVMMetric{Name: "cortex_kvm_project_instances", Labels: labels, Value: value}
}

func kvmCapacityMetric(computeHost, az, projectID, projectName, domainID, domainName, resource string, value float64) collectedKVMMetric {
	labels := mockKVMHostLabels(computeHost, az)
	labels["project_id"] = projectID
	labels["project_name"] = projectName
	labels["domain_id"] = domainID
	labels["domain_name"] = domainName
	labels["resource"] = resource
	return collectedKVMMetric{Name: "cortex_kvm_project_capacity_usage", Labels: labels, Value: value}
}

func buildKVMHypervisorClient(t *testing.T, hvs []hv1.Hypervisor) *fake.ClientBuilder {
	t.Helper()
	s := runtime.NewScheme()
	if err := hv1.AddToScheme(s); err != nil {
		t.Fatalf("failed to add hv1 scheme: %v", err)
	}
	var objects []runtime.Object
	for i := range hvs {
		objects = append(objects, &hvs[i])
	}
	return fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objects...)
}

func TestKVMProjectUtilizationKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	kpi := &KVMProjectUtilizationKPI{}
	if err := kpi.Init(&testDB, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestKVMProjectUtilizationKPI_getKVMHosts(t *testing.T) {
	hvs := []hv1.Hypervisor{
		{ObjectMeta: metav1.ObjectMeta{Name: "node001-bb01"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "node002-bb01"}},
	}

	clientBuilder := buildKVMHypervisorClient(t, hvs)
	kpi := &KVMProjectUtilizationKPI{}
	kpi.Client = clientBuilder.Build()

	hostMapping, err := kpi.getKVMHosts()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(hostMapping) != len(hvs) {
		t.Fatalf("expected %d hosts, got %d", len(hvs), len(hostMapping))
	}
	for _, hv := range hvs {
		host, ok := hostMapping[hv.Name]
		if !ok {
			t.Fatalf("expected host %s not found in mapping", hv.Name)
		}
		if host.Name != hv.Name {
			t.Errorf("host name mismatch: expected %s, got %s", hv.Name, host.Name)
		}
	}
}

func TestKVMProjectUtilizationKPI_queryProjectInstanceCount(t *testing.T) {
	tests := []struct {
		name           string
		servers        []nova.Server
		projects       []identity.Project
		domains        []identity.Domain
		expectedCounts map[string]kvmProjectInstanceCount
	}{
		{
			name: "single instance in one project",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:  []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			expectedCounts: map[string]kvmProjectInstanceCount{
				"project-1|node001-bb01|flavor-1|az1": {ProjectID: "project-1", ProjectName: "Project One", DomainID: "domain-1", DomainName: "Domain One", ComputeHost: "node001-bb01", FlavorName: "flavor-1", AvailabilityZone: "az1", InstanceCount: 1},
			},
		},
		{
			name: "multiple instances across projects and hosts",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "server-2", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-2", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "server-3", TenantID: "project-2", OSEXTSRVATTRHost: "node002-bb02", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az2"},
				{ID: "server-4", TenantID: "project-2", OSEXTSRVATTRHost: "node002-bb02", FlavorName: "flavor-2", Status: "ACTIVE", OSEXTAvailabilityZone: "az2"},
			},
			projects: []identity.Project{
				{ID: "project-1", Name: "Project One", DomainID: "domain-1"},
				{ID: "project-2", Name: "Project Two", DomainID: "domain-1"},
			},
			domains: []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			expectedCounts: map[string]kvmProjectInstanceCount{
				"project-1|node001-bb01|flavor-1|az1": {ProjectID: "project-1", ProjectName: "Project One", DomainID: "domain-1", DomainName: "Domain One", ComputeHost: "node001-bb01", FlavorName: "flavor-1", AvailabilityZone: "az1", InstanceCount: 1},
				"project-1|node001-bb01|flavor-2|az1": {ProjectID: "project-1", ProjectName: "Project One", DomainID: "domain-1", DomainName: "Domain One", ComputeHost: "node001-bb01", FlavorName: "flavor-2", AvailabilityZone: "az1", InstanceCount: 1},
				"project-2|node002-bb02|flavor-1|az2": {ProjectID: "project-2", ProjectName: "Project Two", DomainID: "domain-1", DomainName: "Domain One", ComputeHost: "node002-bb02", FlavorName: "flavor-1", AvailabilityZone: "az2", InstanceCount: 1},
				"project-2|node002-bb02|flavor-2|az2": {ProjectID: "project-2", ProjectName: "Project Two", DomainID: "domain-1", DomainName: "Domain One", ComputeHost: "node002-bb02", FlavorName: "flavor-2", AvailabilityZone: "az2", InstanceCount: 1},
			},
		},
		{
			name: "instances on non-KVM hosts are excluded",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "server-2", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-2", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:  []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			expectedCounts: map[string]kvmProjectInstanceCount{
				"project-1|node001-bb01|flavor-1|az1": {ProjectID: "project-1", ProjectName: "Project One", DomainID: "domain-1", DomainName: "Domain One", ComputeHost: "node001-bb01", FlavorName: "flavor-1", AvailabilityZone: "az1", InstanceCount: 1},
			},
		},
		{
			name: "instances with non-ACTIVE status are excluded",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "DELETED", OSEXTAvailabilityZone: "az1"},
				{ID: "server-2", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-2", Status: "ERROR", OSEXTAvailabilityZone: "az1"},
				{ID: "server-3", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-3", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:  []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			expectedCounts: map[string]kvmProjectInstanceCount{
				"project-1|node001-bb01|flavor-3|az1": {ProjectID: "project-1", ProjectName: "Project One", DomainID: "domain-1", DomainName: "Domain One", ComputeHost: "node001-bb01", FlavorName: "flavor-3", AvailabilityZone: "az1", InstanceCount: 1},
			},
		},
		{
			name: "multiple instances with same key are counted correctly",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "server-2", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "server-3", TenantID: "project-1", OSEXTSRVATTRHost: "node002-bb02", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az2"},
				{ID: "server-4", TenantID: "project-1", OSEXTSRVATTRHost: "node002-bb02", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az2"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:  []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			expectedCounts: map[string]kvmProjectInstanceCount{
				"project-1|node001-bb01|flavor-1|az1": {ProjectID: "project-1", ProjectName: "Project One", DomainID: "domain-1", DomainName: "Domain One", ComputeHost: "node001-bb01", FlavorName: "flavor-1", AvailabilityZone: "az1", InstanceCount: 2},
				"project-1|node002-bb02|flavor-1|az2": {ProjectID: "project-1", ProjectName: "Project One", DomainID: "domain-1", DomainName: "Domain One", ComputeHost: "node002-bb02", FlavorName: "flavor-1", AvailabilityZone: "az2", InstanceCount: 2},
			},
		},
		{
			name: "project references non-existent domain results in empty domain fields",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-unknown"}},
			domains:  []identity.Domain{},
			expectedCounts: map[string]kvmProjectInstanceCount{
				// The domain_id is extracted from the project record, so it should be "domain-unknown" even though there is no matching domain entry
				"project-1|node001-bb01|flavor-1|az1": {ProjectID: "project-1", ProjectName: "Project One", DomainID: "domain-unknown", DomainName: "", ComputeHost: "node001-bb01", FlavorName: "flavor-1", AvailabilityZone: "az1", InstanceCount: 1},
			},
		},
		{
			name: "missing project entry results in empty project_name and domain",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{},
			domains:  []identity.Domain{},
			expectedCounts: map[string]kvmProjectInstanceCount{
				"project-1|node001-bb01|flavor-1|az1": {ProjectID: "project-1", ProjectName: "", DomainID: "", DomainName: "", ComputeHost: "node001-bb01", FlavorName: "flavor-1", AvailabilityZone: "az1", InstanceCount: 1},
			},
		},
		{
			name:           "no instances returns empty result",
			servers:        []nova.Server{},
			projects:       []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:        []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			expectedCounts: map[string]kvmProjectInstanceCount{},
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
				testDB.AddTable(identity.Domain{}),
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
			for i := range tt.domains {
				mockData = append(mockData, &tt.domains[i])
			}
			if len(mockData) > 0 {
				if err := testDB.Insert(mockData...); err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			}

			client := buildKVMHypervisorClient(t, []hv1.Hypervisor{})
			kpi := &KVMProjectUtilizationKPI{}
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

func TestKVMProjectUtilizationKPI_queryProjectCapacityUsage(t *testing.T) {
	tests := []struct {
		name           string
		servers        []nova.Server
		projects       []identity.Project
		domains        []identity.Domain
		flavors        []nova.Flavor
		expectedUsages map[string]kvmProjectCapacityUsage
	}{
		{
			name: "single instance with flavor details",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:  []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors:  []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			expectedUsages: map[string]kvmProjectCapacityUsage{
				"project-1|node001-bb01|az1": {ProjectID: "project-1", ProjectName: "Project One", DomainID: "domain-1", DomainName: "Domain One", ComputeHost: "node001-bb01", AvailabilityZone: "az1", TotalVCPUs: 2, TotalRAMMB: 4096, TotalDiskGB: 1},
			},
		},
		{
			name: "multiple instances with different flavors and projects",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "server-2", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-2", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "server-3", TenantID: "project-2", OSEXTSRVATTRHost: "node002-bb02", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az2"},
			},
			projects: []identity.Project{
				{ID: "project-1", Name: "Project One", DomainID: "domain-1"},
				{ID: "project-2", Name: "Project Two", DomainID: "domain-1"},
			},
			domains: []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1},
				{ID: "f2", Name: "flavor-2", VCPUs: 4, RAM: 8192, Disk: 2},
			},
			expectedUsages: map[string]kvmProjectCapacityUsage{
				"project-1|node001-bb01|az1": {ProjectID: "project-1", ProjectName: "Project One", DomainID: "domain-1", DomainName: "Domain One", ComputeHost: "node001-bb01", AvailabilityZone: "az1", TotalVCPUs: 6, TotalRAMMB: 12288, TotalDiskGB: 3},
				"project-2|node002-bb02|az2": {ProjectID: "project-2", ProjectName: "Project Two", DomainID: "domain-1", DomainName: "Domain One", ComputeHost: "node002-bb02", AvailabilityZone: "az2", TotalVCPUs: 2, TotalRAMMB: 4096, TotalDiskGB: 1},
			},
		},
		{
			name: "missing flavor entry results in zero capacity",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-missing", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:  []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors:  []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			expectedUsages: map[string]kvmProjectCapacityUsage{
				"project-1|node001-bb01|az1": {ProjectID: "project-1", ProjectName: "Project One", DomainID: "domain-1", DomainName: "Domain One", ComputeHost: "node001-bb01", AvailabilityZone: "az1", TotalVCPUs: 0, TotalRAMMB: 0, TotalDiskGB: 0},
			},
		},
		{
			name: "instances on non-KVM hosts are excluded",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects:       []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:        []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors:        []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			expectedUsages: map[string]kvmProjectCapacityUsage{},
		},
		{
			name: "instances with non-ACTIVE status are excluded",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "DELETED", OSEXTAvailabilityZone: "az1"},
			},
			projects:       []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:        []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors:        []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			expectedUsages: map[string]kvmProjectCapacityUsage{},
		},
		{
			name:    "no instances returns empty capacity usage",
			servers: []nova.Server{},
			projects: []identity.Project{
				{ID: "project-1", Name: "Project One", DomainID: "domain-1"},
			},
			domains:        []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors:        []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			expectedUsages: map[string]kvmProjectCapacityUsage{},
		},
		{
			name: "multiple instances with same flavor aggregate capacity correctly",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "server-2", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:  []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors:  []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			expectedUsages: map[string]kvmProjectCapacityUsage{
				"project-1|node001-bb01|az1": {ProjectID: "project-1", ProjectName: "Project One", DomainID: "domain-1", DomainName: "Domain One", ComputeHost: "node001-bb01", AvailabilityZone: "az1", TotalVCPUs: 4, TotalRAMMB: 8192, TotalDiskGB: 2},
			},
		},
		{
			name: "project references non-existent domain results in empty domain fields",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-unknown"}},
			domains:  []identity.Domain{},
			flavors:  []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			expectedUsages: map[string]kvmProjectCapacityUsage{
				// The domain_id is extracted from the project record, so it should be "domain-unknown" even though there is no matching domain entry
				"project-1|node001-bb01|az1": {ProjectID: "project-1", ProjectName: "Project One", DomainID: "domain-unknown", DomainName: "", ComputeHost: "node001-bb01", AvailabilityZone: "az1", TotalVCPUs: 2, TotalRAMMB: 4096, TotalDiskGB: 1},
			},
		},
		{
			name: "missing project entry results in empty project_name and domain",
			servers: []nova.Server{
				{ID: "server-1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{},
			domains:  []identity.Domain{},
			flavors:  []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			expectedUsages: map[string]kvmProjectCapacityUsage{
				"project-1|node001-bb01|az1": {ProjectID: "project-1", ProjectName: "", DomainID: "", DomainName: "", ComputeHost: "node001-bb01", AvailabilityZone: "az1", TotalVCPUs: 2, TotalRAMMB: 4096, TotalDiskGB: 1},
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
				testDB.AddTable(identity.Domain{}),
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
			for i := range tt.domains {
				mockData = append(mockData, &tt.domains[i])
			}
			for i := range tt.flavors {
				mockData = append(mockData, &tt.flavors[i])
			}
			if len(mockData) > 0 {
				if err := testDB.Insert(mockData...); err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			}

			client := buildKVMHypervisorClient(t, []hv1.Hypervisor{})
			kpi := &KVMProjectUtilizationKPI{}
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

func TestKVMProjectUtilizationKPI_Collect(t *testing.T) {
	tests := []struct {
		name            string
		servers         []nova.Server
		projects        []identity.Project
		domains         []identity.Domain
		flavors         []nova.Flavor
		hypervisors     []hv1.Hypervisor
		expectedMetrics []collectedKVMMetric
	}{
		{
			name: "single instance in one project",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:  []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors:  []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			hypervisors: []hv1.Hypervisor{
				{ObjectMeta: metav1.ObjectMeta{Name: "node001-bb01", Labels: map[string]string{"topology.kubernetes.io/zone": "az1"}}},
			},
			expectedMetrics: []collectedKVMMetric{
				kvmInstanceMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "flavor-1", 1),
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "cpu", 2),
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "ram", 4096*1024*1024),
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "disk", 1*1024*1024*1024),
			},
		},
		{
			name: "multiple instances across hosts, projects, and flavors",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "s2", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-2", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "s3", TenantID: "project-2", OSEXTSRVATTRHost: "node002-bb02", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az2"},
			},
			projects: []identity.Project{
				{ID: "project-1", Name: "Project One", DomainID: "domain-1"},
				{ID: "project-2", Name: "Project Two", DomainID: "domain-1"},
			},
			domains: []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1},
				{ID: "f2", Name: "flavor-2", VCPUs: 4, RAM: 8192, Disk: 2},
			},
			hypervisors: []hv1.Hypervisor{
				{ObjectMeta: metav1.ObjectMeta{Name: "node001-bb01", Labels: map[string]string{"topology.kubernetes.io/zone": "az1"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "node002-bb02", Labels: map[string]string{"topology.kubernetes.io/zone": "az2"}}},
			},
			expectedMetrics: []collectedKVMMetric{
				kvmInstanceMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "flavor-1", 1),
				kvmInstanceMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "flavor-2", 1),
				kvmInstanceMetric("node002-bb02", "az2", "project-2", "Project Two", "domain-1", "Domain One", "flavor-1", 1),
				// node001-bb01/project-1: 1*flavor-1 + 1*flavor-2
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "cpu", 6),
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "ram", 12288*1024*1024),
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "disk", 3*1024*1024*1024),
				// node002-bb02/project-2: 1*flavor-1
				kvmCapacityMetric("node002-bb02", "az2", "project-2", "Project Two", "domain-1", "Domain One", "cpu", 2),
				kvmCapacityMetric("node002-bb02", "az2", "project-2", "Project Two", "domain-1", "Domain One", "ram", 4096*1024*1024),
				kvmCapacityMetric("node002-bb02", "az2", "project-2", "Project Two", "domain-1", "Domain One", "disk", 1*1024*1024*1024),
			},
		},
		{
			name: "non-KVM hosts are excluded from metrics",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "s2", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:  []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors:  []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			hypervisors: []hv1.Hypervisor{
				{ObjectMeta: metav1.ObjectMeta{Name: "node001-bb01", Labels: map[string]string{"topology.kubernetes.io/zone": "az1"}}},
			},
			expectedMetrics: []collectedKVMMetric{
				kvmInstanceMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "flavor-1", 1),
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "cpu", 2),
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "ram", 4096*1024*1024),
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "disk", 1*1024*1024*1024),
			},
		},
		{
			name: "DELETED and ERROR instances are excluded",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "DELETED", OSEXTAvailabilityZone: "az1"},
				{ID: "s2", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-2", Status: "ERROR", OSEXTAvailabilityZone: "az1"},
				{ID: "s3", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-3", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:  []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1},
				{ID: "f2", Name: "flavor-2", VCPUs: 4, RAM: 8192, Disk: 2},
				{ID: "f3", Name: "flavor-3", VCPUs: 8, RAM: 16384, Disk: 4},
			},
			hypervisors: []hv1.Hypervisor{
				{ObjectMeta: metav1.ObjectMeta{Name: "node001-bb01", Labels: map[string]string{"topology.kubernetes.io/zone": "az1"}}},
			},
			expectedMetrics: []collectedKVMMetric{
				kvmInstanceMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "flavor-3", 1),
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "cpu", 8),
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "ram", 16384*1024*1024),
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "disk", 4*1024*1024*1024),
			},
		},
		{
			name: "multiple instances with same flavor are aggregated correctly",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "s2", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
				{ID: "s3", TenantID: "project-1", OSEXTSRVATTRHost: "node002-bb02", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az2"},
				{ID: "s4", TenantID: "project-1", OSEXTSRVATTRHost: "node002-bb02", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az2"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:  []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors:  []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			hypervisors: []hv1.Hypervisor{
				{ObjectMeta: metav1.ObjectMeta{Name: "node001-bb01", Labels: map[string]string{"topology.kubernetes.io/zone": "az1"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "node002-bb02", Labels: map[string]string{"topology.kubernetes.io/zone": "az2"}}},
			},
			expectedMetrics: []collectedKVMMetric{
				kvmInstanceMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "flavor-1", 2),
				kvmInstanceMetric("node002-bb02", "az2", "project-1", "Project One", "domain-1", "Domain One", "flavor-1", 2),
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "cpu", 4),
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "ram", 2*4096*1024*1024),
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "disk", 2*1024*1024*1024),
				kvmCapacityMetric("node002-bb02", "az2", "project-1", "Project One", "domain-1", "Domain One", "cpu", 4),
				kvmCapacityMetric("node002-bb02", "az2", "project-1", "Project One", "domain-1", "Domain One", "ram", 2*4096*1024*1024),
				kvmCapacityMetric("node002-bb02", "az2", "project-1", "Project One", "domain-1", "Domain One", "disk", 2*1024*1024*1024),
			},
		},
		{
			name: "compute host not in hypervisor list produces no metrics",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects:        []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:         []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors:         []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			hypervisors:     []hv1.Hypervisor{},
			expectedMetrics: []collectedKVMMetric{},
		},
		{
			name: "project references non-existent domain results in empty domain labels",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-unknown"}},
			domains:  []identity.Domain{},
			flavors:  []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			hypervisors: []hv1.Hypervisor{
				{ObjectMeta: metav1.ObjectMeta{Name: "node001-bb01", Labels: map[string]string{"topology.kubernetes.io/zone": "az1"}}},
			},
			expectedMetrics: []collectedKVMMetric{
				// The domain_id is extracted from the project record, so it should be "domain-unknown" even though there is no matching domain entry
				kvmInstanceMetric("node001-bb01", "az1", "project-1", "Project One", "domain-unknown", "", "flavor-1", 1),
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "Project One", "domain-unknown", "", "cpu", 2),
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "Project One", "domain-unknown", "", "ram", 4096*1024*1024),
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "Project One", "domain-unknown", "", "disk", 1*1024*1024*1024),
			},
		},
		{
			name: "missing project entry results in empty project_name and domain labels",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-1", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{},
			domains:  []identity.Domain{},
			flavors:  []nova.Flavor{{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1}},
			hypervisors: []hv1.Hypervisor{
				{ObjectMeta: metav1.ObjectMeta{Name: "node001-bb01", Labels: map[string]string{"topology.kubernetes.io/zone": "az1"}}},
			},
			expectedMetrics: []collectedKVMMetric{
				kvmInstanceMetric("node001-bb01", "az1", "project-1", "", "", "", "flavor-1", 1),
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "", "", "", "cpu", 2),
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "", "", "", "ram", 4096*1024*1024),
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "", "", "", "disk", 1*1024*1024*1024),
			},
		},
		{
			name: "missing flavor entry results in zero capacity",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "flavor-missing", Status: "ACTIVE", OSEXTAvailabilityZone: "az1"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:  []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors:  []nova.Flavor{},
			hypervisors: []hv1.Hypervisor{
				{ObjectMeta: metav1.ObjectMeta{Name: "node001-bb01", Labels: map[string]string{"topology.kubernetes.io/zone": "az1"}}},
			},
			expectedMetrics: []collectedKVMMetric{
				kvmInstanceMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "flavor-missing", 1),
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "cpu", 0),
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "ram", 0),
				kvmCapacityMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", "disk", 0),
			},
		},
		{
			name:    "no instances produces no metrics",
			servers: []nova.Server{},
			projects: []identity.Project{
				{ID: "project-1", Name: "Project One", DomainID: "domain-1"},
			},
			domains: []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "flavor-1", VCPUs: 2, RAM: 4096, Disk: 1},
			},
			hypervisors: []hv1.Hypervisor{
				{ObjectMeta: metav1.ObjectMeta{Name: "node001-bb01", Labels: map[string]string{"topology.kubernetes.io/zone": "az1"}}},
			},
			expectedMetrics: []collectedKVMMetric{},
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
				testDB.AddTable(identity.Domain{}),
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
			for i := range tt.domains {
				mockData = append(mockData, &tt.domains[i])
			}
			for i := range tt.flavors {
				mockData = append(mockData, &tt.flavors[i])
			}
			if len(mockData) > 0 {
				if err := testDB.Insert(mockData...); err != nil {
					t.Fatalf("expected no error inserting data, got %v", err)
				}
			}

			client := buildKVMHypervisorClient(t, tt.hypervisors)
			kpi := &KVMProjectUtilizationKPI{}
			if err := kpi.Init(&testDB, client.Build(), conf.NewRawOpts("{}")); err != nil {
				t.Fatalf("expected no error on Init, got %v", err)
			}

			ch := make(chan prometheus.Metric, 100)
			kpi.Collect(ch)
			close(ch)

			actual := make(map[string]collectedKVMMetric)
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
				key := buildKVMMetricKey(name, labels)
				if _, exists := actual[key]; exists {
					t.Fatalf("duplicate metric key %q", key)
				}
				actual[key] = collectedKVMMetric{Name: name, Labels: labels, Value: pm.GetGauge().GetValue()}
			}

			if len(actual) != len(tt.expectedMetrics) {
				t.Errorf("expected %d metrics, got %d: actual=%v", len(tt.expectedMetrics), len(actual), actual)
			}
			for _, exp := range tt.expectedMetrics {
				key := buildKVMMetricKey(exp.Name, exp.Labels)
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
