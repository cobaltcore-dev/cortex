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
)

func hanaStackingMetric(computeHost, az, projectID, projectName, domainID, domainName string, value float64) collectedKVMMetric {
	labels := mockKVMHostLabels(computeHost, az)
	labels["project_id"] = projectID
	labels["project_name"] = projectName
	labels["domain_id"] = domainID
	labels["domain_name"] = domainName
	return collectedKVMMetric{Name: "cortex_kvm_hana_stacking_ram_bytes", Labels: labels, Value: value}
}

func setupHanaStackingDB(t *testing.T, servers []nova.Server, projects []identity.Project, domains []identity.Domain, flavors []nova.Flavor) db.DB {
	t.Helper()
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	t.Cleanup(dbEnv.Close)

	if err := testDB.CreateTable(
		testDB.AddTable(nova.Server{}),
		testDB.AddTable(identity.Project{}),
		testDB.AddTable(identity.Domain{}),
		testDB.AddTable(nova.Flavor{}),
	); err != nil {
		t.Fatalf("failed to create tables: %v", err)
	}

	var mockData []any
	for i := range servers {
		mockData = append(mockData, &servers[i])
	}
	for i := range projects {
		mockData = append(mockData, &projects[i])
	}
	for i := range domains {
		mockData = append(mockData, &domains[i])
	}
	for i := range flavors {
		mockData = append(mockData, &flavors[i])
	}
	if len(mockData) > 0 {
		if err := testDB.Insert(mockData...); err != nil {
			t.Fatalf("expected no error inserting data, got %v", err)
		}
	}
	return testDB
}

func TestKVMHanaStackingKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	kpi := &KVMHanaStackingKPI{}
	if err := kpi.Init(&testDB, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestKVMHanaStackingKPI_queryHanaStacking(t *testing.T) {
	tests := []struct {
		name     string
		servers  []nova.Server
		projects []identity.Project
		domains  []identity.Domain
		flavors  []nova.Flavor
		expected map[string]kvmHanaStackingRow
	}{
		{
			name: "single HANA instance",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "hana_k_medium", Status: "ACTIVE"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-0"}},
			domains:  []identity.Domain{{ID: "domain-0", Name: "Domain One"}},
			flavors:  []nova.Flavor{{ID: "f1", Name: "hana_k_medium", RAM: 1638400}},
			expected: map[string]kvmHanaStackingRow{
				"project-1|node001-bb01": {ProjectID: "project-1", ProjectName: "Project One", DomainID: "domain-0", DomainName: "Domain One", ComputeHost: "node001-bb01", TotalRAMMB: 1638400},
			},
		},
		{
			name: "multiple HANA instances same project and host are aggregated",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "hana_k_medium", Status: "ACTIVE"},
				{ID: "s2", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "hana_k_large", Status: "ACTIVE"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:  []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "hana_k_medium", RAM: 1638400},
				{ID: "f2", Name: "hana_k_large", RAM: 3276800},
			},
			expected: map[string]kvmHanaStackingRow{
				"project-1|node001-bb01": {ProjectID: "project-1", ProjectName: "Project One", DomainID: "domain-1", DomainName: "Domain One", ComputeHost: "node001-bb01", TotalRAMMB: 4915200},
			},
		},
		{
			name: "multiple projects on different hosts",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "hana_k_medium", Status: "ACTIVE"},
				{ID: "s2", TenantID: "project-2", OSEXTSRVATTRHost: "node002-bb02", FlavorName: "hana_k_large", Status: "ACTIVE"},
			},
			projects: []identity.Project{
				{ID: "project-1", Name: "Project One", DomainID: "domain-1"},
				{ID: "project-2", Name: "Project Two", DomainID: "domain-1"},
			},
			domains: []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "hana_k_medium", RAM: 1638400},
				{ID: "f2", Name: "hana_k_large", RAM: 3276800},
			},
			expected: map[string]kvmHanaStackingRow{
				"project-1|node001-bb01": {ProjectID: "project-1", ProjectName: "Project One", DomainID: "domain-1", DomainName: "Domain One", ComputeHost: "node001-bb01", TotalRAMMB: 1638400},
				"project-2|node002-bb02": {ProjectID: "project-2", ProjectName: "Project Two", DomainID: "domain-1", DomainName: "Domain One", ComputeHost: "node002-bb02", TotalRAMMB: 3276800},
			},
		},
		{
			name: "non-HANA flavor instances are excluded",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "hana_k_medium", Status: "ACTIVE"},
				{ID: "s2", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "m1_k_large", Status: "ACTIVE"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:  []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "hana_k_medium", RAM: 1638400},
				{ID: "f2", Name: "m1_k_large", RAM: 65536},
			},
			expected: map[string]kvmHanaStackingRow{
				"project-1|node001-bb01": {ProjectID: "project-1", ProjectName: "Project One", DomainID: "domain-1", DomainName: "Domain One", ComputeHost: "node001-bb01", TotalRAMMB: 1638400},
			},
		},
		{
			name: "non-KVM host instances are excluded",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "hana_k_medium", Status: "ACTIVE"},
				{ID: "s2", TenantID: "project-1", OSEXTSRVATTRHost: "nova-compute-1", FlavorName: "hana_k_medium", Status: "ACTIVE"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:  []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors:  []nova.Flavor{{ID: "f1", Name: "hana_k_medium", RAM: 1638400}},
			expected: map[string]kvmHanaStackingRow{
				"project-1|node001-bb01": {ProjectID: "project-1", ProjectName: "Project One", DomainID: "domain-1", DomainName: "Domain One", ComputeHost: "node001-bb01", TotalRAMMB: 1638400},
			},
		},
		{
			name: "DELETED and ERROR instances are excluded",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "hana_k_medium", Status: "DELETED"},
				{ID: "s2", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "hana_k_medium", Status: "ERROR"},
				{ID: "s3", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "hana_k_medium", Status: "ACTIVE"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:  []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors:  []nova.Flavor{{ID: "f1", Name: "hana_k_medium", RAM: 1638400}},
			expected: map[string]kvmHanaStackingRow{
				"project-1|node001-bb01": {ProjectID: "project-1", ProjectName: "Project One", DomainID: "domain-1", DomainName: "Domain One", ComputeHost: "node001-bb01", TotalRAMMB: 1638400},
			},
		},
		{
			name:     "no instances returns empty result",
			servers:  []nova.Server{},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:  []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors:  []nova.Flavor{{ID: "f1", Name: "hana_k_medium", RAM: 1638400}},
			expected: map[string]kvmHanaStackingRow{},
		},
		{
			name: "missing flavor entry results in zero RAM",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "hana_k_unknown", Status: "ACTIVE"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:  []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors:  []nova.Flavor{},
			expected: map[string]kvmHanaStackingRow{
				"project-1|node001-bb01": {ProjectID: "project-1", ProjectName: "Project One", DomainID: "domain-1", DomainName: "Domain One", ComputeHost: "node001-bb01", TotalRAMMB: 0},
			},
		},
		{
			name: "missing project entry results in empty strings",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "hana_k_medium", Status: "ACTIVE"},
			},
			projects: []identity.Project{},
			domains:  []identity.Domain{},
			flavors:  []nova.Flavor{{ID: "f1", Name: "hana_k_medium", RAM: 1638400}},
			expected: map[string]kvmHanaStackingRow{
				"project-1|node001-bb01": {ProjectID: "project-1", ProjectName: "", DomainID: "", DomainName: "", ComputeHost: "node001-bb01", TotalRAMMB: 1638400},
			},
		},
		{
			name: "project with unknown domain results in empty domain name",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "hana_k_medium", Status: "ACTIVE"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-unknown"}},
			domains:  []identity.Domain{},
			flavors:  []nova.Flavor{{ID: "f1", Name: "hana_k_medium", RAM: 1638400}},
			expected: map[string]kvmHanaStackingRow{
				"project-1|node001-bb01": {ProjectID: "project-1", ProjectName: "Project One", DomainID: "domain-unknown", DomainName: "", ComputeHost: "node001-bb01", TotalRAMMB: 1638400},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testDB := setupHanaStackingDB(t, tt.servers, tt.projects, tt.domains, tt.flavors)

			kpi := &KVMHanaStackingKPI{}
			if err := kpi.Init(&testDB, buildKVMHypervisorClient(t, []hv1.Hypervisor{}).Build(), conf.NewRawOpts("{}")); err != nil {
				t.Fatalf("expected no error on Init, got %v", err)
			}

			rows, err := kpi.queryHanaStacking()
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if len(rows) != len(tt.expected) {
				t.Fatalf("expected %d rows, got %d", len(tt.expected), len(rows))
			}
			for _, got := range rows {
				key := got.ProjectID + "|" + got.ComputeHost
				exp, ok := tt.expected[key]
				if !ok {
					t.Errorf("unexpected row for key %q: %+v", key, got)
					continue
				}
				if got != exp {
					t.Errorf("row mismatch for key %q: expected %+v, got %+v", key, exp, got)
				}
			}
		})
	}
}

func TestKVMHanaStackingKPI_Collect(t *testing.T) {
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
			name: "single HANA instance produces one RAM metric",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "hana_k_medium", Status: "ACTIVE"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:  []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors:  []nova.Flavor{{ID: "f1", Name: "hana_k_medium", RAM: 1638400}},
			hypervisors: []hv1.Hypervisor{
				{ObjectMeta: metav1.ObjectMeta{Name: "node001-bb01", Labels: map[string]string{"topology.kubernetes.io/zone": "az1"}}},
			},
			expectedMetrics: []collectedKVMMetric{
				hanaStackingMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", 1638400*1024*1024),
			},
		},
		{
			name: "compute_host not in hypervisor list produces no metric",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "hana_k_medium", Status: "ACTIVE"},
			},
			projects:        []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:         []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors:         []nova.Flavor{{ID: "f1", Name: "hana_k_medium", RAM: 1638400}},
			hypervisors:     []hv1.Hypervisor{},
			expectedMetrics: []collectedKVMMetric{},
		},
		{
			name: "only HANA flavors are counted, non-HANA on same host excluded",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "hana_k_medium", Status: "ACTIVE"},
				{ID: "s2", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "m1_k_large", Status: "ACTIVE"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:  []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "hana_k_medium", RAM: 1638400},
				{ID: "f2", Name: "m1_k_large", RAM: 65536},
			},
			hypervisors: []hv1.Hypervisor{
				{ObjectMeta: metav1.ObjectMeta{Name: "node001-bb01", Labels: map[string]string{"topology.kubernetes.io/zone": "az1"}}},
			},
			expectedMetrics: []collectedKVMMetric{
				hanaStackingMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", 1638400*1024*1024),
			},
		},
		{
			name: "DELETED and ERROR instances are excluded",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "hana_k_medium", Status: "DELETED"},
				{ID: "s2", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "hana_k_medium", Status: "ERROR"},
				{ID: "s3", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "hana_k_medium", Status: "ACTIVE"},
			},
			projects: []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:  []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors:  []nova.Flavor{{ID: "f1", Name: "hana_k_medium", RAM: 1638400}},
			hypervisors: []hv1.Hypervisor{
				{ObjectMeta: metav1.ObjectMeta{Name: "node001-bb01", Labels: map[string]string{"topology.kubernetes.io/zone": "az1"}}},
			},
			expectedMetrics: []collectedKVMMetric{
				hanaStackingMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", 1638400*1024*1024),
			},
		},
		{
			name: "multiple projects on multiple hosts",
			servers: []nova.Server{
				{ID: "s1", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "hana_k_medium", Status: "ACTIVE"},
				{ID: "s2", TenantID: "project-1", OSEXTSRVATTRHost: "node001-bb01", FlavorName: "hana_k_medium", Status: "ACTIVE"},
				{ID: "s3", TenantID: "project-2", OSEXTSRVATTRHost: "node002-bb02", FlavorName: "hana_k_large", Status: "ACTIVE"},
			},
			projects: []identity.Project{
				{ID: "project-1", Name: "Project One", DomainID: "domain-1"},
				{ID: "project-2", Name: "Project Two", DomainID: "domain-1"},
			},
			domains: []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "hana_k_medium", RAM: 1638400},
				{ID: "f2", Name: "hana_k_large", RAM: 3276800},
			},
			hypervisors: []hv1.Hypervisor{
				{ObjectMeta: metav1.ObjectMeta{Name: "node001-bb01", Labels: map[string]string{"topology.kubernetes.io/zone": "az1"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "node002-bb02", Labels: map[string]string{"topology.kubernetes.io/zone": "az2"}}},
			},
			expectedMetrics: []collectedKVMMetric{
				hanaStackingMetric("node001-bb01", "az1", "project-1", "Project One", "domain-1", "Domain One", 2*1638400*1024*1024),
				hanaStackingMetric("node002-bb02", "az2", "project-2", "Project Two", "domain-1", "Domain One", 3276800*1024*1024),
			},
		},
		{
			name:            "no instances produces no metrics",
			servers:         []nova.Server{},
			projects:        []identity.Project{{ID: "project-1", Name: "Project One", DomainID: "domain-1"}},
			domains:         []identity.Domain{{ID: "domain-1", Name: "Domain One"}},
			flavors:         []nova.Flavor{{ID: "f1", Name: "hana_k_medium", RAM: 1638400}},
			hypervisors:     []hv1.Hypervisor{{ObjectMeta: metav1.ObjectMeta{Name: "node001-bb01"}}},
			expectedMetrics: []collectedKVMMetric{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testDB := setupHanaStackingDB(t, tt.servers, tt.projects, tt.domains, tt.flavors)

			client := buildKVMHypervisorClient(t, tt.hypervisors)
			kpi := &KVMHanaStackingKPI{}
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
				key := name + "|" + labels["compute_host"] + "|" + labels["project_id"]
				if _, exists := actual[key]; exists {
					t.Fatalf("duplicate metric key %q", key)
				}
				actual[key] = collectedKVMMetric{Name: name, Labels: labels, Value: pm.GetGauge().GetValue()}
			}

			if len(actual) != len(tt.expectedMetrics) {
				t.Errorf("expected %d metrics, got %d: actual=%v", len(tt.expectedMetrics), len(actual), actual)
			}
			for _, exp := range tt.expectedMetrics {
				key := exp.Name + "|" + exp.Labels["compute_host"] + "|" + exp.Labels["project_id"]
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
