// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package infrastructure

import (
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	testlibDB "github.com/cobaltcore-dev/cortex/internal/knowledge/db/testing"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	prometheusgo "github.com/prometheus/client_model/go"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func buildHostCapacityClient(t *testing.T, hostDetails []compute.HostDetails, utilizations []compute.HostUtilization) *fake.ClientBuilder {
	t.Helper()
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("failed to build scheme: %v", err)
	}
	rawDetails, err := v1alpha1.BoxFeatureList(hostDetails)
	if err != nil {
		t.Fatalf("failed to box host details: %v", err)
	}
	rawUtils, err := v1alpha1.BoxFeatureList(utilizations)
	if err != nil {
		t.Fatalf("failed to box host utilizations: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(
		&v1alpha1.Knowledge{
			ObjectMeta: v1.ObjectMeta{Name: hostDetailsKnowledgeName},
			Status:     v1alpha1.KnowledgeStatus{Raw: rawDetails},
		},
		&v1alpha1.Knowledge{
			ObjectMeta: v1.ObjectMeta{Name: hostUtilizationKnowledgeName},
			Status:     v1alpha1.KnowledgeStatus{Raw: rawUtils},
		},
	)
}

func TestVMwareHostCapacityKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	kpi := &VMwareHostCapacityKPI{}
	if err := kpi.Init(&testDB, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestVMwareHostCapacityKPI_getVMwareHosts(t *testing.T) {
	hostDetails := []compute.HostDetails{
		{ComputeHost: "nova-compute-1", HypervisorFamily: hypervisorFamilyVMware},
		{ComputeHost: "nova-compute-2", HypervisorFamily: hypervisorFamilyVMware},
		{ComputeHost: "nova-compute-ironic-1", HypervisorType: vmwareIronicHypervisorType, HypervisorFamily: hypervisorFamilyVMware},
		{ComputeHost: "nova-compute-3", HypervisorFamily: "other"},
	}

	client := buildHostCapacityClient(t, hostDetails, nil)
	kpi := &VMwareHostCapacityKPI{}
	kpi.Client = client.Build()

	hosts, err := kpi.getVMwareHosts()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(hosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(hosts))
	}
	seen := make(map[string]bool)
	for _, h := range hosts {
		seen[h.ComputeHost] = true
	}
	for _, name := range []string{"nova-compute-1", "nova-compute-2"} {
		if !seen[name] {
			t.Errorf("expected host %q in result", name)
		}
	}
}

func TestVMwareHostCapacityKPI_getHostUtilizations(t *testing.T) {
	tests := []struct {
		name          string
		utilizations  []compute.HostUtilization
		expectedHosts []string
	}{
		{
			name: "normal utilizations are returned",
			utilizations: []compute.HostUtilization{
				{ComputeHost: "h1", TotalVCPUsAllocatable: 10, TotalRAMAllocatableMB: 1024, TotalDiskAllocatableGB: 100},
				{ComputeHost: "h2", TotalVCPUsAllocatable: 20, TotalRAMAllocatableMB: 2048, TotalDiskAllocatableGB: 200},
			},
			expectedHosts: []string{"h1", "h2"},
		},
		{
			name: "zero TotalVCPUsAllocatable is skipped",
			utilizations: []compute.HostUtilization{
				{ComputeHost: "h1", TotalVCPUsAllocatable: 0, TotalRAMAllocatableMB: 1024, TotalDiskAllocatableGB: 100},
			},
			expectedHosts: []string{},
		},
		{
			name: "zero TotalRAMAllocatableMB is skipped",
			utilizations: []compute.HostUtilization{
				{ComputeHost: "h1", TotalVCPUsAllocatable: 10, TotalRAMAllocatableMB: 0, TotalDiskAllocatableGB: 100},
			},
			expectedHosts: []string{},
		},
		{
			name: "zero TotalDiskAllocatableGB is skipped",
			utilizations: []compute.HostUtilization{
				{ComputeHost: "h1", TotalVCPUsAllocatable: 10, TotalRAMAllocatableMB: 1024, TotalDiskAllocatableGB: 0},
			},
			expectedHosts: []string{},
		},
		{
			name: "mix of valid and zero-allocatable entries",
			utilizations: []compute.HostUtilization{
				{ComputeHost: "h1", TotalVCPUsAllocatable: 10, TotalRAMAllocatableMB: 1024, TotalDiskAllocatableGB: 100},
				{ComputeHost: "h2", TotalVCPUsAllocatable: 0, TotalRAMAllocatableMB: 1024, TotalDiskAllocatableGB: 100},
			},
			expectedHosts: []string{"h1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := buildHostCapacityClient(t, nil, tt.utilizations)
			kpi := &VMwareHostCapacityKPI{}
			kpi.Client = client.Build()

			m, err := kpi.getHostUtilizations()
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if len(m) != len(tt.expectedHosts) {
				t.Fatalf("expected %d entries, got %d: %v", len(tt.expectedHosts), len(m), m)
			}
			for _, host := range tt.expectedHosts {
				if _, ok := m[host]; !ok {
					t.Errorf("expected host %q in result", host)
				}
			}
		})
	}
}

func TestVMwareHostCapacityKPI_Collect(t *testing.T) {
	tests := []struct {
		name            string
		hostDetails     []compute.HostDetails
		utilizations    []compute.HostUtilization
		expectedMetrics []collectedVMwareMetric
	}{
		{
			name: "single host emits usage and total metrics",
			hostDetails: []compute.HostDetails{
				{ComputeHost: "nova-compute-1", HypervisorFamily: hypervisorFamilyVMware, AvailabilityZone: "az1"},
			},
			utilizations: []compute.HostUtilization{
				{
					ComputeHost:            "nova-compute-1",
					VCPUsUsed:              4,
					TotalVCPUsAllocatable:  16,
					RAMUsedMB:              2048,
					TotalRAMAllocatableMB:  8192,
					DiskUsedGB:             50,
					TotalDiskAllocatableGB: 500,
				},
			},
			expectedMetrics: []collectedVMwareMetric{
				{Name: "cortex_vmware_host_capacity_usage", Labels: hostCapacityLabels("nova-compute-1", "az1", "cpu"), Value: 4},
				{Name: "cortex_vmware_host_capacity_usage", Labels: hostCapacityLabels("nova-compute-1", "az1", "ram"), Value: 2048 * 1024 * 1024},
				{Name: "cortex_vmware_host_capacity_usage", Labels: hostCapacityLabels("nova-compute-1", "az1", "disk"), Value: 50 * 1024 * 1024 * 1024},
				{Name: "cortex_vmware_host_capacity_total", Labels: hostCapacityLabels("nova-compute-1", "az1", "cpu"), Value: 16},
				{Name: "cortex_vmware_host_capacity_total", Labels: hostCapacityLabels("nova-compute-1", "az1", "ram"), Value: 8192 * 1024 * 1024},
				{Name: "cortex_vmware_host_capacity_total", Labels: hostCapacityLabels("nova-compute-1", "az1", "disk"), Value: 500 * 1024 * 1024 * 1024},
			},
		},
		{
			name: "multiple hosts each emit their own metrics",
			hostDetails: []compute.HostDetails{
				{ComputeHost: "nova-compute-1", HypervisorFamily: hypervisorFamilyVMware, AvailabilityZone: "az1"},
				{ComputeHost: "nova-compute-2", HypervisorFamily: hypervisorFamilyVMware, AvailabilityZone: "az2"},
			},
			utilizations: []compute.HostUtilization{
				{ComputeHost: "nova-compute-1", VCPUsUsed: 2, TotalVCPUsAllocatable: 8, RAMUsedMB: 512, TotalRAMAllocatableMB: 2048, DiskUsedGB: 10, TotalDiskAllocatableGB: 100},
				{ComputeHost: "nova-compute-2", VCPUsUsed: 6, TotalVCPUsAllocatable: 12, RAMUsedMB: 1024, TotalRAMAllocatableMB: 4096, DiskUsedGB: 20, TotalDiskAllocatableGB: 200},
			},
			expectedMetrics: []collectedVMwareMetric{
				{Name: "cortex_vmware_host_capacity_usage", Labels: hostCapacityLabels("nova-compute-1", "az1", "cpu"), Value: 2},
				{Name: "cortex_vmware_host_capacity_usage", Labels: hostCapacityLabels("nova-compute-1", "az1", "ram"), Value: 512 * 1024 * 1024},
				{Name: "cortex_vmware_host_capacity_usage", Labels: hostCapacityLabels("nova-compute-1", "az1", "disk"), Value: 10 * 1024 * 1024 * 1024},
				{Name: "cortex_vmware_host_capacity_total", Labels: hostCapacityLabels("nova-compute-1", "az1", "cpu"), Value: 8},
				{Name: "cortex_vmware_host_capacity_total", Labels: hostCapacityLabels("nova-compute-1", "az1", "ram"), Value: 2048 * 1024 * 1024},
				{Name: "cortex_vmware_host_capacity_total", Labels: hostCapacityLabels("nova-compute-1", "az1", "disk"), Value: 100 * 1024 * 1024 * 1024},
				{Name: "cortex_vmware_host_capacity_usage", Labels: hostCapacityLabels("nova-compute-2", "az2", "cpu"), Value: 6},
				{Name: "cortex_vmware_host_capacity_usage", Labels: hostCapacityLabels("nova-compute-2", "az2", "ram"), Value: 1024 * 1024 * 1024},
				{Name: "cortex_vmware_host_capacity_usage", Labels: hostCapacityLabels("nova-compute-2", "az2", "disk"), Value: 20 * 1024 * 1024 * 1024},
				{Name: "cortex_vmware_host_capacity_total", Labels: hostCapacityLabels("nova-compute-2", "az2", "cpu"), Value: 12},
				{Name: "cortex_vmware_host_capacity_total", Labels: hostCapacityLabels("nova-compute-2", "az2", "ram"), Value: 4096 * 1024 * 1024},
				{Name: "cortex_vmware_host_capacity_total", Labels: hostCapacityLabels("nova-compute-2", "az2", "disk"), Value: 200 * 1024 * 1024 * 1024},
			},
		},
		{
			name: "ironic hosts are excluded",
			hostDetails: []compute.HostDetails{
				{ComputeHost: "nova-compute-1", HypervisorFamily: hypervisorFamilyVMware, AvailabilityZone: "az1"},
				{ComputeHost: "nova-compute-ironic-1", HypervisorType: vmwareIronicHypervisorType, HypervisorFamily: hypervisorFamilyVMware, AvailabilityZone: "az1"},
			},
			utilizations: []compute.HostUtilization{
				{ComputeHost: "nova-compute-1", VCPUsUsed: 2, TotalVCPUsAllocatable: 8, RAMUsedMB: 512, TotalRAMAllocatableMB: 2048, DiskUsedGB: 10, TotalDiskAllocatableGB: 100},
				{ComputeHost: "nova-compute-ironic-1", VCPUsUsed: 4, TotalVCPUsAllocatable: 16, RAMUsedMB: 1024, TotalRAMAllocatableMB: 4096, DiskUsedGB: 20, TotalDiskAllocatableGB: 200},
			},
			expectedMetrics: []collectedVMwareMetric{
				{Name: "cortex_vmware_host_capacity_usage", Labels: hostCapacityLabels("nova-compute-1", "az1", "cpu"), Value: 2},
				{Name: "cortex_vmware_host_capacity_usage", Labels: hostCapacityLabels("nova-compute-1", "az1", "ram"), Value: 512 * 1024 * 1024},
				{Name: "cortex_vmware_host_capacity_usage", Labels: hostCapacityLabels("nova-compute-1", "az1", "disk"), Value: 10 * 1024 * 1024 * 1024},
				{Name: "cortex_vmware_host_capacity_total", Labels: hostCapacityLabels("nova-compute-1", "az1", "cpu"), Value: 8},
				{Name: "cortex_vmware_host_capacity_total", Labels: hostCapacityLabels("nova-compute-1", "az1", "ram"), Value: 2048 * 1024 * 1024},
				{Name: "cortex_vmware_host_capacity_total", Labels: hostCapacityLabels("nova-compute-1", "az1", "disk"), Value: 100 * 1024 * 1024 * 1024},
			},
		},
		{
			name: "non-vmware hosts are excluded",
			hostDetails: []compute.HostDetails{
				{ComputeHost: "nova-compute-1", HypervisorFamily: hypervisorFamilyVMware, AvailabilityZone: "az1"},
				{ComputeHost: "nova-compute-2", HypervisorFamily: "kvm", AvailabilityZone: "az1"},
			},
			utilizations: []compute.HostUtilization{
				{ComputeHost: "nova-compute-1", VCPUsUsed: 2, TotalVCPUsAllocatable: 8, RAMUsedMB: 512, TotalRAMAllocatableMB: 2048, DiskUsedGB: 10, TotalDiskAllocatableGB: 100},
				{ComputeHost: "nova-compute-2", VCPUsUsed: 4, TotalVCPUsAllocatable: 16, RAMUsedMB: 1024, TotalRAMAllocatableMB: 4096, DiskUsedGB: 20, TotalDiskAllocatableGB: 200},
			},
			expectedMetrics: []collectedVMwareMetric{
				{Name: "cortex_vmware_host_capacity_usage", Labels: hostCapacityLabels("nova-compute-1", "az1", "cpu"), Value: 2},
				{Name: "cortex_vmware_host_capacity_usage", Labels: hostCapacityLabels("nova-compute-1", "az1", "ram"), Value: 512 * 1024 * 1024},
				{Name: "cortex_vmware_host_capacity_usage", Labels: hostCapacityLabels("nova-compute-1", "az1", "disk"), Value: 10 * 1024 * 1024 * 1024},
				{Name: "cortex_vmware_host_capacity_total", Labels: hostCapacityLabels("nova-compute-1", "az1", "cpu"), Value: 8},
				{Name: "cortex_vmware_host_capacity_total", Labels: hostCapacityLabels("nova-compute-1", "az1", "ram"), Value: 2048 * 1024 * 1024},
				{Name: "cortex_vmware_host_capacity_total", Labels: hostCapacityLabels("nova-compute-1", "az1", "disk"), Value: 100 * 1024 * 1024 * 1024},
			},
		},
		{
			name: "host without matching utilization produces no metrics",
			hostDetails: []compute.HostDetails{
				{ComputeHost: "nova-compute-1", HypervisorFamily: hypervisorFamilyVMware, AvailabilityZone: "az1"},
			},
			utilizations:    []compute.HostUtilization{},
			expectedMetrics: []collectedVMwareMetric{},
		},
		{
			name: "utilization with zero allocatable resources is skipped",
			hostDetails: []compute.HostDetails{
				{ComputeHost: "nova-compute-1", HypervisorFamily: hypervisorFamilyVMware, AvailabilityZone: "az1"},
			},
			utilizations: []compute.HostUtilization{
				{ComputeHost: "nova-compute-1", VCPUsUsed: 2, TotalVCPUsAllocatable: 0, RAMUsedMB: 512, TotalRAMAllocatableMB: 2048, DiskUsedGB: 10, TotalDiskAllocatableGB: 100},
			},
			expectedMetrics: []collectedVMwareMetric{},
		},
		{
			name:            "no hosts produces no metrics",
			hostDetails:     []compute.HostDetails{},
			utilizations:    []compute.HostUtilization{},
			expectedMetrics: []collectedVMwareMetric{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbEnv := testlibDB.SetupDBEnv(t)
			testDB := db.DB{DbMap: dbEnv.DbMap}
			defer dbEnv.Close()

			client := buildHostCapacityClient(t, tt.hostDetails, tt.utilizations)
			kpi := &VMwareHostCapacityKPI{}
			if err := kpi.Init(&testDB, client.Build(), conf.NewRawOpts("{}")); err != nil {
				t.Fatalf("expected no error on Init, got %v", err)
			}

			ch := make(chan prometheus.Metric, 200)
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
				key := name + "|" + labels["compute_host"] + "|" + labels["resource"]
				if _, exists := actual[key]; exists {
					t.Fatalf("duplicate metric key %q", key)
				}
				actual[key] = collectedVMwareMetric{Name: name, Labels: labels, Value: pm.GetGauge().GetValue()}
			}

			if len(actual) != len(tt.expectedMetrics) {
				t.Errorf("expected %d metrics, got %d: actual=%v", len(tt.expectedMetrics), len(actual), actual)
			}
			for _, exp := range tt.expectedMetrics {
				key := exp.Name + "|" + exp.Labels["compute_host"] + "|" + exp.Labels["resource"]
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

func hostCapacityLabels(computeHost, az, resource string) map[string]string {
	labels := hostLabels(computeHost, az)
	labels["resource"] = resource
	return labels
}
