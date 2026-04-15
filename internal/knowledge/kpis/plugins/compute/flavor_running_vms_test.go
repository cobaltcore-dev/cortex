// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/identity"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	testlibDB "github.com/cobaltcore-dev/cortex/internal/knowledge/db/testing"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	prometheusgo "github.com/prometheus/client_model/go"
)

func TestFlavorRunningVMsKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	kpi := &FlavorRunningVMsKPI{}
	if err := kpi.Init(&testDB, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestFlavorRunningVMsKPI_Collect(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	if err := testDB.CreateTable(
		testDB.AddTable(nova.Server{}),
		testDB.AddTable(identity.Project{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	mockData := []any{
		// VMware flavor (no "_k_" segment)
		&nova.Server{
			ID:                    "id-1",
			FlavorName:            "small_vmware_flavor",
			OSEXTAvailabilityZone: "zone1",
			TenantID:              "project-1",
		},
		// KVM flavor ("_k_" as second segment)
		&nova.Server{
			ID:                    "id-2",
			FlavorName:            "medium_k_flavor",
			OSEXTAvailabilityZone: "zone1",
			TenantID:              "project-1",
		},
		&nova.Server{
			ID:                    "id-3",
			FlavorName:            "medium_k_flavor",
			OSEXTAvailabilityZone: "zone1",
			TenantID:              "project-1",
		},
		// Another VMware flavor in a different zone and project
		&nova.Server{
			ID:                    "id-4",
			FlavorName:            "large_vmware_flavor",
			OSEXTAvailabilityZone: "zone2",
			TenantID:              "project-2",
		},

		&identity.Project{
			ID:   "project-1",
			Name: "Project One",
		},
		&identity.Project{
			ID:   "project-2",
			Name: "Project Two",
		},
	}

	if err := testDB.Insert(mockData...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &FlavorRunningVMsKPI{}
	if err := kpi.Init(&testDB, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan prometheus.Metric, 100)
	kpi.Collect(ch)
	close(ch)

	type FlavorRunningVMsMetric struct {
		FlavorName       string
		AvailabilityZone string
		RunningVMs       float64
		ProjectID        string
		ProjectName      string
		HypervisorFamily string
	}

	metrics := make(map[string]FlavorRunningVMsMetric)

	for metric := range ch {
		var m prometheusgo.Metric
		if err := metric.Write(&m); err != nil {
			t.Fatalf("failed to write metric: %v", err)
		}

		labels := make(map[string]string)
		for _, label := range m.Label {
			labels[label.GetName()] = label.GetValue()
		}

		flavor := labels["flavor_name"]
		availabilityZone := labels["availability_zone"]
		projectID := labels["project_id"]
		projectName := labels["project_name"]
		hypervisorFamily := labels["hypervisor_family"]

		key := flavor + "|" + availabilityZone + "|" + projectID

		metrics[key] = FlavorRunningVMsMetric{
			FlavorName:       flavor,
			AvailabilityZone: availabilityZone,
			ProjectID:        projectID,
			ProjectName:      projectName,
			RunningVMs:       m.GetGauge().GetValue(),
			HypervisorFamily: hypervisorFamily,
		}
	}

	expectedMetrics := map[string]FlavorRunningVMsMetric{
		"small_vmware_flavor|zone1|project-1": {
			FlavorName:       "small_vmware_flavor",
			AvailabilityZone: "zone1",
			ProjectID:        "project-1",
			ProjectName:      "Project One",
			RunningVMs:       1,
			HypervisorFamily: "vmware",
		},
		"medium_k_flavor|zone1|project-1": {
			FlavorName:       "medium_k_flavor",
			AvailabilityZone: "zone1",
			ProjectID:        "project-1",
			ProjectName:      "Project One",
			RunningVMs:       2,
			HypervisorFamily: "kvm",
		},
		"large_vmware_flavor|zone2|project-2": {
			FlavorName:       "large_vmware_flavor",
			AvailabilityZone: "zone2",
			ProjectID:        "project-2",
			ProjectName:      "Project Two",
			RunningVMs:       1,
			HypervisorFamily: "vmware",
		},
	}

	for key, expected := range expectedMetrics {
		actual, ok := metrics[key]
		if !ok {
			t.Errorf("expected metric %q not found", key)
			continue
		}
		if !reflect.DeepEqual(expected, actual) {
			t.Errorf("metric %q: expected %+v, got %+v", key, expected, actual)
		}
	}
}

func TestKVMFlavorPattern(t *testing.T) {
	tests := []struct {
		flavor string
		isKVM  bool
	}{
		{"x_k_c89_m7890_v2", true},
		{"x_k_c89_m7890", true},
		{"x_v_c12_m3456", false},
		{"x_kvm_c12_m3456", false}, // "kvm" != "k"
		{"k_c12_m3456", false},     // "k" must be second segment
		{"", false},
	}
	for _, tc := range tests {
		got := kvmFlavorPattern.MatchString(tc.flavor)
		if got != tc.isKVM {
			t.Errorf("kvmFlavorPattern.MatchString(%q) = %v, want %v", tc.flavor, got, tc.isKVM)
		}
	}
}
