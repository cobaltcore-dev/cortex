// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/limes"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	testlibDB "github.com/cobaltcore-dev/cortex/internal/knowledge/db/testing"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	prometheusgo "github.com/prometheus/client_model/go"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestVMwareResourceCommitmentsKPI_CollectHanaUnusedCommitments(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error building scheme, got %v", err)
	}

	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()

	if err := testDB.CreateTable(
		testDB.AddTable(limes.Commitment{}),
		testDB.AddTable(nova.Flavor{}),
		testDB.AddTable(nova.Server{}),
	); err != nil {
		t.Fatalf("expected no error creating tables, got %v", err)
	}

	// Flavors: hana_small (4 vcpu, 16384 MB ram, 100 GB disk)
	//          hana_large_v2 (16 vcpu, 65536 MB ram, 400 GB disk)
	if err := testDB.Insert(
		&nova.Flavor{ID: "f1", Name: "hana_small", VCPUs: 4, RAM: 16384, Disk: 100},
		&nova.Flavor{ID: "f2", Name: "hana_large_v2", VCPUs: 16, RAM: 65536, Disk: 400},
		&nova.Flavor{ID: "f3", Name: "general_medium", VCPUs: 8, RAM: 32768, Disk: 200},
	); err != nil {
		t.Fatalf("expected no error inserting flavors, got %v", err)
	}

	// Commitments across two AZs to verify per-AZ aggregation:
	//   project-A: 3 x hana_small in az1 (cascade-lake)
	//   project-B: 2 x hana_large_v2 in az1 (sapphire-rapids)
	//   project-A: 4 x hana_small in az2 (cascade-lake) — separate AZ bucket
	//   project-C: 1 x hana_k_foo in az1  — hana_k_ prefix, should be skipped
	//   project-D: 1 x general_medium     — not hana_, should be skipped
	//   project-A: 10 x hana_small pending — should be excluded (wrong status)
	if err := testDB.Insert(
		&limes.Commitment{ID: 1, ServiceType: "compute", ResourceName: "instances_hana_small", AvailabilityZone: "az1", Amount: 3, Status: "confirmed", ProjectID: "project-A"},
		&limes.Commitment{ID: 2, ServiceType: "compute", ResourceName: "instances_hana_large_v2", AvailabilityZone: "az1", Amount: 2, Status: "confirmed", ProjectID: "project-B"},
		&limes.Commitment{ID: 3, ServiceType: "compute", ResourceName: "instances_hana_small", AvailabilityZone: "az2", Amount: 4, Status: "guaranteed", ProjectID: "project-A"},
		&limes.Commitment{ID: 4, ServiceType: "compute", ResourceName: "instances_hana_k_foo", AvailabilityZone: "az1", Amount: 5, Status: "confirmed", ProjectID: "project-C"},
		&limes.Commitment{ID: 5, ServiceType: "compute", ResourceName: "instances_general_medium", AvailabilityZone: "az1", Amount: 1, Status: "confirmed", ProjectID: "project-D"},
		&limes.Commitment{ID: 6, ServiceType: "compute", ResourceName: "instances_hana_small", AvailabilityZone: "az1", Amount: 10, Status: "pending", ProjectID: "project-A"},
	); err != nil {
		t.Fatalf("expected no error inserting commitments, got %v", err)
	}

	// Running servers:
	//   project-A/az1: 1 hana_small ACTIVE, 1 DELETED (ignored) → 2 unused in az1
	//   project-B/az1: 0 hana_large_v2                          → 2 unused in az1
	//   project-A/az2: 1 hana_small ACTIVE                      → 3 unused in az2
	if err := testDB.Insert(
		&nova.Server{ID: "s1", TenantID: "project-A", FlavorName: "hana_small", OSEXTAvailabilityZone: "az1", Status: "ACTIVE"},
		&nova.Server{ID: "s2", TenantID: "project-A", FlavorName: "hana_small", OSEXTAvailabilityZone: "az1", Status: "DELETED"},
		&nova.Server{ID: "s3", TenantID: "project-A", FlavorName: "hana_small", OSEXTAvailabilityZone: "az2", Status: "ACTIVE"},
	); err != nil {
		t.Fatalf("expected no error inserting servers, got %v", err)
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(
			&v1alpha1.Knowledge{ObjectMeta: v1.ObjectMeta{Name: "host-details"}},
		).
		Build()

	kpi := &VMwareResourceCommitmentsKPI{}
	if err := kpi.Init(&testDB, k8sClient, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan prometheus.Metric, 100)
	kpi.Collect(ch)
	close(ch)

	type UnusedMetric struct {
		Resource string
		AZ       string
		Arch     string
		Value    float64
	}

	actual := make(map[string]UnusedMetric)
	for metric := range ch {
		if getMetricName(metric.Desc().String()) != "cortex_vmware_hana_unused_instance_commitments" {
			continue
		}
		var m prometheusgo.Metric
		if err := metric.Write(&m); err != nil {
			t.Fatalf("failed to write metric: %v", err)
		}
		labels := make(map[string]string)
		for _, lbl := range m.Label {
			labels[lbl.GetName()] = lbl.GetValue()
		}
		key := labels["resource"] + "/" + labels["availability_zone"] + "/" + labels["cpu_architecture"]
		actual[key] = UnusedMetric{
			Resource: labels["resource"],
			AZ:       labels["availability_zone"],
			Arch:     labels["cpu_architecture"],
			Value:    m.GetGauge().GetValue(),
		}
	}

	// project-A/az1: 2 unused hana_small (cascade-lake)      → cpu=2×4=8,  ram=2×16384=32768,  disk=2×100=200
	// project-B/az1: 2 unused hana_large_v2 (sapphire-rapids) → cpu=2×16=32, ram=2×65536=131072, disk=2×400=800
	// project-A/az2: 3 unused hana_small (cascade-lake)      → cpu=3×4=12, ram=3×16384=49152,  disk=3×100=300
	expected := map[string]UnusedMetric{
		"cpu/az1/cascade-lake":     {Resource: "cpu", AZ: "az1", Arch: "cascade-lake", Value: 8},
		"ram/az1/cascade-lake":     {Resource: "ram", AZ: "az1", Arch: "cascade-lake", Value: 32768},
		"disk/az1/cascade-lake":    {Resource: "disk", AZ: "az1", Arch: "cascade-lake", Value: 200},
		"cpu/az1/sapphire-rapids":  {Resource: "cpu", AZ: "az1", Arch: "sapphire-rapids", Value: 32},
		"ram/az1/sapphire-rapids":  {Resource: "ram", AZ: "az1", Arch: "sapphire-rapids", Value: 131072},
		"disk/az1/sapphire-rapids": {Resource: "disk", AZ: "az1", Arch: "sapphire-rapids", Value: 800},
		"cpu/az2/cascade-lake":     {Resource: "cpu", AZ: "az2", Arch: "cascade-lake", Value: 12},
		"ram/az2/cascade-lake":     {Resource: "ram", AZ: "az2", Arch: "cascade-lake", Value: 49152},
		"disk/az2/cascade-lake":    {Resource: "disk", AZ: "az2", Arch: "cascade-lake", Value: 300},
	}

	if len(actual) != len(expected) {
		t.Errorf("expected %d metrics, got %d: %v", len(expected), len(actual), actual)
	}
	for key, exp := range expected {
		got, ok := actual[key]
		if !ok {
			t.Errorf("missing metric %q", key)
			continue
		}
		if !reflect.DeepEqual(exp, got) {
			t.Errorf("metric %q: expected %+v, got %+v", key, exp, got)
		}
	}
}
