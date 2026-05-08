// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package infrastructure

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/identity"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/limes"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	testlibDB "github.com/cobaltcore-dev/cortex/internal/knowledge/db/testing"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	prometheusgo "github.com/prometheus/client_model/go"
)

func setupProjectCommitmentsDB(t *testing.T) (testDB *db.DB, cleanup func()) {
	t.Helper()
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB = &db.DB{DbMap: dbEnv.DbMap}
	if err := testDB.CreateTable(
		testDB.AddTable(limes.Commitment{}),
		testDB.AddTable(nova.Server{}),
		testDB.AddTable(nova.Flavor{}),
		testDB.AddTable(identity.Project{}),
		testDB.AddTable(identity.Domain{}),
	); err != nil {
		t.Fatalf("failed to create tables: %v", err)
	}
	return testDB, dbEnv.Close
}

// collectProjectCommitmentsMetrics runs the KPI and returns all emitted metrics keyed by
// "metricName|az|cpu_architecture|resource|project_id|project_name|domain_id|domain_name".
// GP metrics have an empty cpu_architecture segment since the descriptor does not include that label.
func collectProjectCommitmentsMetrics(t *testing.T, testDB *db.DB) map[string]float64 {
	t.Helper()
	kpi := &VMwareProjectCommitmentsKPI{}
	if err := kpi.Init(testDB, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("failed to init KPI: %v", err)
	}
	ch := make(chan prometheus.Metric, 200)
	kpi.Collect(ch)
	close(ch)

	result := make(map[string]float64)
	for m := range ch {
		var pm prometheusgo.Metric
		if err := m.Write(&pm); err != nil {
			t.Fatalf("failed to write metric: %v", err)
		}
		lbls := make(map[string]string)
		for _, lp := range pm.Label {
			lbls[lp.GetName()] = lp.GetValue()
		}
		name := getMetricName(m.Desc().String())
		key := name + "|" + lbls["availability_zone"] + "|" + lbls["cpu_architecture"] + "|" + lbls["resource"] + "|" + lbls["project_id"] + "|" + lbls["project_name"] + "|" + lbls["domain_id"] + "|" + lbls["domain_name"]
		result[key] = pm.GetGauge().GetValue()
	}
	return result
}

// gpKey builds the expected map key for a general-purpose metric.
// cpu_architecture is always empty because the GP metric descriptor omits that label.
func gpKey(az, resource string, p projectWithDomain) string {
	return "cortex_vmware_unused_commitments_general_purpose|" + az + "||" + resource + "|" + p.ProjectID + "|" + p.ProjectName + "|" + p.DomainID + "|" + p.DomainName
}

// hKey builds the expected map key for a HANA metric.
func hKey(az, cpuArch, resource string, p projectWithDomain) string {
	return "cortex_vmware_unused_commitments_hana_resources|" + az + "|" + cpuArch + "|" + resource + "|" + p.ProjectID + "|" + p.ProjectName + "|" + p.DomainID + "|" + p.DomainName
}

func TestVMwareProjectCommitmentsKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	kpi := &VMwareProjectCommitmentsKPI{}
	if err := kpi.Init(&testDB, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestVMwareProjectCommitmentsKPI_Collect_GeneralPurpose(t *testing.T) {
	// Reusable project/domain entries for test cases that need them.
	p1 := identity.Project{ID: "p1", Name: "project-one", DomainID: "d1", Enabled: true}
	p2 := identity.Project{ID: "p2", Name: "project-two", DomainID: "d1", Enabled: true}
	d1 := identity.Domain{ID: "d1", Name: "domain-one", Enabled: true}
	pd1 := projectWithDomain{ProjectID: "p1", ProjectName: "project-one", DomainID: "d1", DomainName: "domain-one"}
	pd2 := projectWithDomain{ProjectID: "p2", ProjectName: "project-two", DomainID: "d1", DomainName: "domain-one"}

	tests := []struct {
		name        string
		commitments []limes.Commitment
		servers     []nova.Server
		flavors     []nova.Flavor
		projects    []identity.Project
		domains     []identity.Domain
		want        map[string]float64
	}{
		{
			name: "no commitments produces no metrics",
			want: map[string]float64{},
		},
		{
			name: "fully unused cores commitment",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "c1", ServiceType: "compute", ResourceName: "cores", AvailabilityZone: "az1", Amount: 10, Status: "confirmed", ProjectID: "p1"},
			},
			projects: []identity.Project{p1},
			domains:  []identity.Domain{d1},
			want: map[string]float64{
				gpKey("az1", "cpu", pd1): 10,
			},
		},
		{
			name: "fully unused ram commitment with MiB unit",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "c1", ServiceType: "compute", ResourceName: "ram", AvailabilityZone: "az1", Amount: 1024, Unit: "MiB", Status: "confirmed", ProjectID: "p1"},
			},
			projects: []identity.Project{p1},
			domains:  []identity.Domain{d1},
			want: map[string]float64{
				gpKey("az1", "ram", pd1): 1024 * 1024 * 1024,
			},
		},
		{
			name: "fully unused ram commitment with GiB unit",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "c1", ServiceType: "compute", ResourceName: "ram", AvailabilityZone: "az1", Amount: 2, Unit: "GiB", Status: "confirmed", ProjectID: "p1"},
			},
			projects: []identity.Project{p1},
			domains:  []identity.Domain{d1},
			want: map[string]float64{
				gpKey("az1", "ram", pd1): 2 * 1024 * 1024 * 1024,
			},
		},
		{
			name: "partial cpu usage reduces unused",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "c1", ServiceType: "compute", ResourceName: "cores", AvailabilityZone: "az1", Amount: 10, Status: "confirmed", ProjectID: "p1"},
			},
			servers: []nova.Server{
				{ID: "s1", TenantID: "p1", FlavorName: "small", OSEXTAvailabilityZone: "az1", Status: "ACTIVE"},
				{ID: "s2", TenantID: "p1", FlavorName: "small", OSEXTAvailabilityZone: "az1", Status: "ACTIVE"},
			},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "small", VCPUs: 3, RAM: 0, Disk: 0},
			},
			projects: []identity.Project{p1},
			domains:  []identity.Domain{d1},
			want: map[string]float64{
				gpKey("az1", "cpu", pd1): 4, // 10 - 2×3 = 4
			},
		},
		{
			name: "fully covered cpu produces no metric",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "c1", ServiceType: "compute", ResourceName: "cores", AvailabilityZone: "az1", Amount: 4, Status: "confirmed", ProjectID: "p1"},
			},
			servers: []nova.Server{
				{ID: "s1", TenantID: "p1", FlavorName: "small", OSEXTAvailabilityZone: "az1", Status: "ACTIVE"},
			},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "small", VCPUs: 4, RAM: 0, Disk: 0},
			},
			projects: []identity.Project{p1},
			domains:  []identity.Domain{d1},
			want:     map[string]float64{},
		},
		{
			name: "over-used cpu produces no metric",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "c1", ServiceType: "compute", ResourceName: "cores", AvailabilityZone: "az1", Amount: 2, Status: "confirmed", ProjectID: "p1"},
			},
			servers: []nova.Server{
				{ID: "s1", TenantID: "p1", FlavorName: "large", OSEXTAvailabilityZone: "az1", Status: "ACTIVE"},
			},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "large", VCPUs: 8, RAM: 0, Disk: 0},
			},
			projects: []identity.Project{p1},
			domains:  []identity.Domain{d1},
			want:     map[string]float64{},
		},
		{
			name: "hana servers not counted against gp commitments",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "c1", ServiceType: "compute", ResourceName: "cores", AvailabilityZone: "az1", Amount: 10, Status: "confirmed", ProjectID: "p1"},
			},
			servers: []nova.Server{
				{ID: "s1", TenantID: "p1", FlavorName: "hana_small", OSEXTAvailabilityZone: "az1", Status: "ACTIVE"},
			},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "hana_small", VCPUs: 8, RAM: 0, Disk: 0},
			},
			projects: []identity.Project{p1},
			domains:  []identity.Domain{d1},
			want: map[string]float64{
				gpKey("az1", "cpu", pd1): 10,
			},
		},
		{
			name: "kvm servers not counted against gp commitments",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "c1", ServiceType: "compute", ResourceName: "cores", AvailabilityZone: "az1", Amount: 10, Status: "confirmed", ProjectID: "p1"},
			},
			servers: []nova.Server{
				{ID: "s1", TenantID: "p1", FlavorName: "m1_k_small", OSEXTAvailabilityZone: "az1", Status: "ACTIVE"},
			},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "m1_k_small", VCPUs: 4, RAM: 0, Disk: 0},
			},
			projects: []identity.Project{p1},
			domains:  []identity.Domain{d1},
			want: map[string]float64{
				gpKey("az1", "cpu", pd1): 10,
			},
		},
		{
			name: "DELETED and ERROR servers excluded from usage",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "c1", ServiceType: "compute", ResourceName: "cores", AvailabilityZone: "az1", Amount: 10, Status: "confirmed", ProjectID: "p1"},
			},
			servers: []nova.Server{
				{ID: "s1", TenantID: "p1", FlavorName: "small", OSEXTAvailabilityZone: "az1", Status: "DELETED"},
				{ID: "s2", TenantID: "p1", FlavorName: "small", OSEXTAvailabilityZone: "az1", Status: "ERROR"},
				{ID: "s3", TenantID: "p1", FlavorName: "small", OSEXTAvailabilityZone: "az1", Status: "ACTIVE"},
			},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "small", VCPUs: 2, RAM: 0, Disk: 0},
			},
			projects: []identity.Project{p1},
			domains:  []identity.Domain{d1},
			want: map[string]float64{
				gpKey("az1", "cpu", pd1): 8, // only 1 ACTIVE × 2 subtracted
			},
		},
		{
			name: "guaranteed commitments counted",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "c1", ServiceType: "compute", ResourceName: "cores", AvailabilityZone: "az1", Amount: 5, Status: "guaranteed", ProjectID: "p1"},
			},
			projects: []identity.Project{p1},
			domains:  []identity.Domain{d1},
			want: map[string]float64{
				gpKey("az1", "cpu", pd1): 5,
			},
		},
		{
			name: "pending commitments excluded",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "c1", ServiceType: "compute", ResourceName: "cores", AvailabilityZone: "az1", Amount: 100, Status: "pending", ProjectID: "p1"},
			},
			projects: []identity.Project{p1},
			domains:  []identity.Domain{d1},
			want:     map[string]float64{},
		},
		{
			name: "non-compute service type excluded",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "c1", ServiceType: "network", ResourceName: "cores", AvailabilityZone: "az1", Amount: 100, Status: "confirmed", ProjectID: "p1"},
			},
			projects: []identity.Project{p1},
			domains:  []identity.Domain{d1},
			want:     map[string]float64{},
		},
		{
			name: "multiple commitments per project and AZ summed",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "c1", ServiceType: "compute", ResourceName: "cores", AvailabilityZone: "az1", Amount: 10, Status: "confirmed", ProjectID: "p1"},
				{ID: 2, UUID: "c2", ServiceType: "compute", ResourceName: "cores", AvailabilityZone: "az1", Amount: 5, Status: "confirmed", ProjectID: "p1"},
				{ID: 3, UUID: "c3", ServiceType: "compute", ResourceName: "cores", AvailabilityZone: "az2", Amount: 20, Status: "confirmed", ProjectID: "p1"},
				{ID: 4, UUID: "c4", ServiceType: "compute", ResourceName: "cores", AvailabilityZone: "az1", Amount: 8, Status: "confirmed", ProjectID: "p2"},
			},
			projects: []identity.Project{p1, p2},
			domains:  []identity.Domain{d1},
			want: map[string]float64{
				gpKey("az1", "cpu", pd1): 15,
				gpKey("az2", "cpu", pd1): 20,
				gpKey("az1", "cpu", pd2): 8,
			},
		},
		{
			name: "cpu and ram unused reported separately",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "c1", ServiceType: "compute", ResourceName: "cores", AvailabilityZone: "az1", Amount: 8, Status: "confirmed", ProjectID: "p1"},
				{ID: 2, UUID: "c2", ServiceType: "compute", ResourceName: "ram", AvailabilityZone: "az1", Amount: 512, Unit: "MiB", Status: "confirmed", ProjectID: "p1"},
			},
			servers: []nova.Server{
				{ID: "s1", TenantID: "p1", FlavorName: "medium", OSEXTAvailabilityZone: "az1", Status: "ACTIVE"},
			},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "medium", VCPUs: 2, RAM: 256, Disk: 0},
			},
			projects: []identity.Project{p1},
			domains:  []identity.Domain{d1},
			want: map[string]float64{
				gpKey("az1", "cpu", pd1): 6,                         // 8 - 1×2
				gpKey("az1", "ram", pd1): (512 - 256) * 1024 * 1024, // 512MiB - 256MB (flavor.RAM is in MB)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testDB, cleanup := setupProjectCommitmentsDB(t)
			defer cleanup()

			var rows []any
			for i := range tt.commitments {
				rows = append(rows, &tt.commitments[i])
			}
			for i := range tt.servers {
				rows = append(rows, &tt.servers[i])
			}
			for i := range tt.flavors {
				rows = append(rows, &tt.flavors[i])
			}
			for i := range tt.projects {
				rows = append(rows, &tt.projects[i])
			}
			for i := range tt.domains {
				rows = append(rows, &tt.domains[i])
			}
			if len(rows) > 0 {
				if err := testDB.Insert(rows...); err != nil {
					t.Fatalf("failed to insert test data: %v", err)
				}
			}

			got := collectProjectCommitmentsMetrics(t, testDB)

			if len(got) != len(tt.want) {
				t.Errorf("expected %d metrics, got %d: %v", len(tt.want), len(got), got)
			}
			for k, wantVal := range tt.want {
				gotVal, ok := got[k]
				if !ok {
					t.Errorf("missing metric %q", k)
					continue
				}
				if gotVal != wantVal {
					t.Errorf("metric %q: expected %f, got %f", k, wantVal, gotVal)
				}
			}
		})
	}
}

func TestVMwareProjectCommitmentsKPI_Collect_HANA(t *testing.T) {
	// Reusable project/domain entries for test cases that need them.
	p1 := identity.Project{ID: "p1", Name: "project-one", DomainID: "d1", Enabled: true}
	p2 := identity.Project{ID: "p2", Name: "project-two", DomainID: "d1", Enabled: true}
	d1 := identity.Domain{ID: "d1", Name: "domain-one", Enabled: true}
	pd1 := projectWithDomain{ProjectID: "p1", ProjectName: "project-one", DomainID: "d1", DomainName: "domain-one"}
	pd2 := projectWithDomain{ProjectID: "p2", ProjectName: "project-two", DomainID: "d1", DomainName: "domain-one"}

	tests := []struct {
		name        string
		commitments []limes.Commitment
		servers     []nova.Server
		flavors     []nova.Flavor
		projects    []identity.Project
		domains     []identity.Domain
		want        map[string]float64
	}{
		{
			name: "no commitments produces no metrics",
			want: map[string]float64{},
		},
		{
			name: "fully unused hana instance commitment",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "h1", ServiceType: "compute", ResourceName: "instances_hana_c128_m1600", AvailabilityZone: "az1", Amount: 2, Status: "confirmed", ProjectID: "p1"},
			},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "hana_c128_m1600", VCPUs: 128, RAM: 1638400, Disk: 100},
			},
			projects: []identity.Project{p1},
			domains:  []identity.Domain{d1},
			want: map[string]float64{
				hKey("az1", "cascade-lake", "cpu", pd1):  2 * 128,
				hKey("az1", "cascade-lake", "ram", pd1):  2 * 1638400 * 1024 * 1024,
				hKey("az1", "cascade-lake", "disk", pd1): 2 * 100 * 1024 * 1024 * 1024,
			},
		},
		{
			name: "partial hana usage reduces unused instances",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "h1", ServiceType: "compute", ResourceName: "instances_hana_c128_m1600", AvailabilityZone: "az1", Amount: 3, Status: "confirmed", ProjectID: "p1"},
			},
			servers: []nova.Server{
				{ID: "s1", TenantID: "p1", FlavorName: "hana_c128_m1600", OSEXTAvailabilityZone: "az1", Status: "ACTIVE"},
			},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "hana_c128_m1600", VCPUs: 128, RAM: 1638400, Disk: 100},
			},
			projects: []identity.Project{p1},
			domains:  []identity.Domain{d1},
			want: map[string]float64{
				hKey("az1", "cascade-lake", "cpu", pd1):  2 * 128,
				hKey("az1", "cascade-lake", "ram", pd1):  2 * 1638400 * 1024 * 1024,
				hKey("az1", "cascade-lake", "disk", pd1): 2 * 100 * 1024 * 1024 * 1024,
			},
		},
		{
			name: "fully used hana produces no metric",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "h1", ServiceType: "compute", ResourceName: "instances_hana_small", AvailabilityZone: "az1", Amount: 2, Status: "confirmed", ProjectID: "p1"},
			},
			servers: []nova.Server{
				{ID: "s1", TenantID: "p1", FlavorName: "hana_small", OSEXTAvailabilityZone: "az1", Status: "ACTIVE"},
				{ID: "s2", TenantID: "p1", FlavorName: "hana_small", OSEXTAvailabilityZone: "az1", Status: "ACTIVE"},
			},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "hana_small", VCPUs: 64, RAM: 819200, Disk: 50},
			},
			projects: []identity.Project{p1},
			domains:  []identity.Domain{d1},
			want:     map[string]float64{},
		},
		{
			name: "over-used hana produces no metric",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "h1", ServiceType: "compute", ResourceName: "instances_hana_small", AvailabilityZone: "az1", Amount: 1, Status: "confirmed", ProjectID: "p1"},
			},
			servers: []nova.Server{
				{ID: "s1", TenantID: "p1", FlavorName: "hana_small", OSEXTAvailabilityZone: "az1", Status: "ACTIVE"},
				{ID: "s2", TenantID: "p1", FlavorName: "hana_small", OSEXTAvailabilityZone: "az1", Status: "ACTIVE"},
			},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "hana_small", VCPUs: 64, RAM: 819200, Disk: 50},
			},
			projects: []identity.Project{p1},
			domains:  []identity.Domain{d1},
			want:     map[string]float64{},
		},
		{
			name: "sapphire-rapids arch from _v2 suffix",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "h1", ServiceType: "compute", ResourceName: "instances_hana_c256_m3200_v2", AvailabilityZone: "az1", Amount: 1, Status: "confirmed", ProjectID: "p1"},
			},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "hana_c256_m3200_v2", VCPUs: 256, RAM: 3276800, Disk: 200},
			},
			projects: []identity.Project{p1},
			domains:  []identity.Domain{d1},
			want: map[string]float64{
				hKey("az1", "sapphire-rapids", "cpu", pd1):  256,
				hKey("az1", "sapphire-rapids", "ram", pd1):  3276800 * 1024 * 1024,
				hKey("az1", "sapphire-rapids", "disk", pd1): 200 * 1024 * 1024 * 1024,
			},
		},
		{
			name: "cascade-lake and sapphire-rapids aggregated separately",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "h1", ServiceType: "compute", ResourceName: "instances_hana_c128_m1600", AvailabilityZone: "az1", Amount: 2, Status: "confirmed", ProjectID: "p1"},
				{ID: 2, UUID: "h2", ServiceType: "compute", ResourceName: "instances_hana_c128_m1600_v2", AvailabilityZone: "az1", Amount: 1, Status: "confirmed", ProjectID: "p1"},
			},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "hana_c128_m1600", VCPUs: 128, RAM: 1638400, Disk: 100},
				{ID: "f2", Name: "hana_c128_m1600_v2", VCPUs: 128, RAM: 1638400, Disk: 100},
			},
			projects: []identity.Project{p1},
			domains:  []identity.Domain{d1},
			want: map[string]float64{
				hKey("az1", "cascade-lake", "cpu", pd1):     2 * 128,
				hKey("az1", "cascade-lake", "ram", pd1):     2 * 1638400 * 1024 * 1024,
				hKey("az1", "cascade-lake", "disk", pd1):    2 * 100 * 1024 * 1024 * 1024,
				hKey("az1", "sapphire-rapids", "cpu", pd1):  1 * 128,
				hKey("az1", "sapphire-rapids", "ram", pd1):  1 * 1638400 * 1024 * 1024,
				hKey("az1", "sapphire-rapids", "disk", pd1): 1 * 100 * 1024 * 1024 * 1024,
			},
		},
		{
			name: "kvm hana commitments excluded",
			commitments: []limes.Commitment{
				// hana_k_large is a KVM HANA flavor — must be filtered out
				{ID: 1, UUID: "h1", ServiceType: "compute", ResourceName: "instances_hana_k_large", AvailabilityZone: "az1", Amount: 5, Status: "confirmed", ProjectID: "p1"},
			},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "hana_k_large", VCPUs: 64, RAM: 819200, Disk: 50},
			},
			projects: []identity.Project{p1},
			domains:  []identity.Domain{d1},
			want:     map[string]float64{},
		},
		{
			name: "DELETED and ERROR hana servers excluded from running count",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "h1", ServiceType: "compute", ResourceName: "instances_hana_small", AvailabilityZone: "az1", Amount: 3, Status: "confirmed", ProjectID: "p1"},
			},
			servers: []nova.Server{
				{ID: "s1", TenantID: "p1", FlavorName: "hana_small", OSEXTAvailabilityZone: "az1", Status: "DELETED"},
				{ID: "s2", TenantID: "p1", FlavorName: "hana_small", OSEXTAvailabilityZone: "az1", Status: "ERROR"},
				{ID: "s3", TenantID: "p1", FlavorName: "hana_small", OSEXTAvailabilityZone: "az1", Status: "ACTIVE"},
			},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "hana_small", VCPUs: 64, RAM: 819200, Disk: 50},
			},
			projects: []identity.Project{p1},
			domains:  []identity.Domain{d1},
			want: map[string]float64{
				hKey("az1", "cascade-lake", "cpu", pd1):  2 * 64, // 3 committed - 1 ACTIVE = 2 unused
				hKey("az1", "cascade-lake", "ram", pd1):  2 * 819200 * 1024 * 1024,
				hKey("az1", "cascade-lake", "disk", pd1): 2 * 50 * 1024 * 1024 * 1024,
			},
		},
		{
			name: "guaranteed hana commitments counted",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "h1", ServiceType: "compute", ResourceName: "instances_hana_small", AvailabilityZone: "az1", Amount: 1, Status: "guaranteed", ProjectID: "p1"},
			},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "hana_small", VCPUs: 64, RAM: 819200, Disk: 50},
			},
			projects: []identity.Project{p1},
			domains:  []identity.Domain{d1},
			want: map[string]float64{
				hKey("az1", "cascade-lake", "cpu", pd1):  64,
				hKey("az1", "cascade-lake", "ram", pd1):  819200 * 1024 * 1024,
				hKey("az1", "cascade-lake", "disk", pd1): 50 * 1024 * 1024 * 1024,
			},
		},
		{
			name: "unknown flavor is skipped without panic",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "h1", ServiceType: "compute", ResourceName: "instances_hana_nonexistent", AvailabilityZone: "az1", Amount: 2, Status: "confirmed", ProjectID: "p1"},
			},
			projects: []identity.Project{p1},
			domains:  []identity.Domain{d1},
			want:     map[string]float64{},
		},
		{
			name: "multiple projects and AZs aggregated per bucket",
			commitments: []limes.Commitment{
				{ID: 1, UUID: "h1", ServiceType: "compute", ResourceName: "instances_hana_small", AvailabilityZone: "az1", Amount: 2, Status: "confirmed", ProjectID: "p1"},
				{ID: 2, UUID: "h2", ServiceType: "compute", ResourceName: "instances_hana_small", AvailabilityZone: "az2", Amount: 3, Status: "confirmed", ProjectID: "p1"},
				{ID: 3, UUID: "h3", ServiceType: "compute", ResourceName: "instances_hana_small", AvailabilityZone: "az1", Amount: 1, Status: "confirmed", ProjectID: "p2"},
			},
			flavors: []nova.Flavor{
				{ID: "f1", Name: "hana_small", VCPUs: 64, RAM: 819200, Disk: 50},
			},
			projects: []identity.Project{p1, p2},
			domains:  []identity.Domain{d1},
			want: map[string]float64{
				hKey("az1", "cascade-lake", "cpu", pd1):  2 * 64,
				hKey("az1", "cascade-lake", "ram", pd1):  2 * 819200 * 1024 * 1024,
				hKey("az1", "cascade-lake", "disk", pd1): 2 * 50 * 1024 * 1024 * 1024,
				hKey("az2", "cascade-lake", "cpu", pd1):  3 * 64,
				hKey("az2", "cascade-lake", "ram", pd1):  3 * 819200 * 1024 * 1024,
				hKey("az2", "cascade-lake", "disk", pd1): 3 * 50 * 1024 * 1024 * 1024,
				hKey("az1", "cascade-lake", "cpu", pd2):  1 * 64,
				hKey("az1", "cascade-lake", "ram", pd2):  1 * 819200 * 1024 * 1024,
				hKey("az1", "cascade-lake", "disk", pd2): 1 * 50 * 1024 * 1024 * 1024,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testDB, cleanup := setupProjectCommitmentsDB(t)
			defer cleanup()

			var rows []any
			for i := range tt.commitments {
				rows = append(rows, &tt.commitments[i])
			}
			for i := range tt.servers {
				rows = append(rows, &tt.servers[i])
			}
			for i := range tt.flavors {
				rows = append(rows, &tt.flavors[i])
			}
			for i := range tt.projects {
				rows = append(rows, &tt.projects[i])
			}
			for i := range tt.domains {
				rows = append(rows, &tt.domains[i])
			}
			if len(rows) > 0 {
				if err := testDB.Insert(rows...); err != nil {
					t.Fatalf("failed to insert test data: %v", err)
				}
			}

			got := collectProjectCommitmentsMetrics(t, testDB)

			if len(got) != len(tt.want) {
				t.Errorf("expected %d metrics, got %d: %v", len(tt.want), len(got), got)
			}
			for k, wantVal := range tt.want {
				gotVal, ok := got[k]
				if !ok {
					t.Errorf("missing metric %q", k)
					continue
				}
				if gotVal != wantVal {
					t.Errorf("metric %q: expected %f, got %f", k, wantVal, gotVal)
				}
			}
		})
	}
}
