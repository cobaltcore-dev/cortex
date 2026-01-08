// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	testlib "github.com/cobaltcore-dev/cortex/pkg/testing"
	"github.com/prometheus/client_golang/prometheus"
	prometheusgo "github.com/prometheus/client_model/go"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestKVMResourceCapacityKPI_Init(t *testing.T) {
	kpi := &KVMResourceCapacityKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestKVMResourceCapacityKPI_Collect_TotalMetric(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hostDetails, err := v1alpha1.BoxFeatureList([]any{
		&compute.HostDetails{
			ComputeHost:      "kvm-host",
			AvailabilityZone: "az2",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "qemu",
			HypervisorFamily: "kvm",
			WorkloadType:     "hana",
			Enabled:          false,
			Decommissioned:   false,
			ExternalCustomer: false,
			DisabledReason:   testlib.Ptr("test"),
			PinnedProjects:   testlib.Ptr("project1,project2"),
		},
		// Skip this because it's not a KVM host
		&compute.HostDetails{
			ComputeHost:      "vmware-host",
			AvailabilityZone: "az1",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "vcenter",
			HypervisorFamily: "vmware",
			WorkloadType:     "general-purpose",
			Enabled:          true,
			Decommissioned:   true,
			ExternalCustomer: true,
			DisabledReason:   nil,
			PinnedProjects:   nil,
		},
		// Skip this because placement doesn't report any capacity for this host
		&compute.HostDetails{
			ComputeHost:      "kvm-host-2",
			AvailabilityZone: "az2",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "qemu",
			HypervisorFamily: "kvm",
			WorkloadType:     "hana",
			Enabled:          false,
			Decommissioned:   false,
			ExternalCustomer: false,
			DisabledReason:   testlib.Ptr("test"),
			PinnedProjects:   testlib.Ptr("project1,project2"),
		},
		// Skip this because it's a ironic host
		&compute.HostDetails{
			ComputeHost:      "ironic-host",
			AvailabilityZone: "az2",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "ironic",
			HypervisorFamily: "vmware",
			WorkloadType:     "hana",
			Enabled:          false,
			Decommissioned:   false,
			ExternalCustomer: false,
			DisabledReason:   testlib.Ptr("test"),
			PinnedProjects:   testlib.Ptr("project1"),
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hostUtilizations, err := v1alpha1.BoxFeatureList([]any{
		&compute.HostUtilization{
			ComputeHost:            "vmware-host",
			TotalVCPUsAllocatable:  100,
			TotalRAMAllocatableMB:  200,
			TotalDiskAllocatableGB: 300,
			VCPUsUsed:              40,
			RAMUsedMB:              100,
			DiskUsedGB:             30,
		},
		&compute.HostUtilization{
			ComputeHost:            "kvm-host",
			TotalVCPUsAllocatable:  100,
			TotalRAMAllocatableMB:  100,
			TotalDiskAllocatableGB: 100,
			VCPUsUsed:              75,
			RAMUsedMB:              80,
			DiskUsedGB:             85,
		},
		&compute.HostUtilization{
			ComputeHost:            "ironic-host",
			TotalVCPUsAllocatable:  0,
			TotalRAMAllocatableMB:  0,
			TotalDiskAllocatableGB: 0,
			VCPUsUsed:              0,
			RAMUsedMB:              0,
			DiskUsedGB:             0,
		},
		// No Capacity reported for host kvm-host-2
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &KVMResourceCapacityKPI{}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(&v1alpha1.Knowledge{
			ObjectMeta: v1.ObjectMeta{Name: "host-details"},
			Status:     v1alpha1.KnowledgeStatus{Raw: hostDetails},
		}, &v1alpha1.Knowledge{
			ObjectMeta: v1.ObjectMeta{Name: "host-utilization"},
			Status:     v1alpha1.KnowledgeStatus{Raw: hostUtilizations},
		}).
		Build()
	if err := kpi.Init(nil, client, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan prometheus.Metric, 100)
	kpi.Collect(ch)
	close(ch)

	type HostResourceMetric struct {
		ComputeHost      string
		Resource         string
		AvailabilityZone string
		Enabled          string
		Decommissioned   string
		ExternalCustomer string
		CPUArchitecture  string
		WorkloadType     string
		Maintenance      string
		Value            float64
	}

	actualMetrics := make(map[string]HostResourceMetric, 0)

	for metric := range ch {
		desc := metric.Desc().String()
		metricName := getMetricName(desc)

		// Only consider cortex_kvm_host_capacity_total metric in this test
		if metricName != "cortex_kvm_host_capacity_total" {
			continue
		}

		var m prometheusgo.Metric
		if err := metric.Write(&m); err != nil {
			t.Fatalf("failed to write metric: %v", err)
		}

		labels := make(map[string]string)
		for _, label := range m.Label {
			labels[label.GetName()] = label.GetValue()
		}

		key := labels["compute_host"] + "-" + labels["resource"]

		actualMetrics[key] = HostResourceMetric{
			ComputeHost:      labels["compute_host"],
			Resource:         labels["resource"],
			AvailabilityZone: labels["availability_zone"],
			Enabled:          labels["enabled"],
			Decommissioned:   labels["decommissioned"],
			ExternalCustomer: labels["external_customer"],
			CPUArchitecture:  labels["cpu_architecture"],
			WorkloadType:     labels["workload_type"],
			Maintenance:      labels["maintenance"],
			Value:            m.GetGauge().GetValue(),
		}
	}

	expectedMetrics := map[string]HostResourceMetric{
		"kvm-host-cpu": {
			ComputeHost:      "kvm-host",
			Resource:         "cpu",
			AvailabilityZone: "az2",
			Enabled:          "false",
			Decommissioned:   "false",
			ExternalCustomer: "false",
			CPUArchitecture:  "cascade-lake",
			WorkloadType:     "hana",
			Maintenance:      "false",
			Value:            100,
		},
		"kvm-host-ram": {
			ComputeHost:      "kvm-host",
			Resource:         "ram",
			AvailabilityZone: "az2",
			Enabled:          "false",
			Decommissioned:   "false",
			ExternalCustomer: "false",
			Maintenance:      "false",
			CPUArchitecture:  "cascade-lake",
			WorkloadType:     "hana",
			Value:            100,
		},
		"kvm-host-disk": {
			ComputeHost:      "kvm-host",
			Resource:         "disk",
			AvailabilityZone: "az2",
			Enabled:          "false",
			Decommissioned:   "false",
			ExternalCustomer: "false",
			CPUArchitecture:  "cascade-lake",
			WorkloadType:     "hana",
			Maintenance:      "false",
			Value:            100,
		},
	}

	if len(expectedMetrics) != len(actualMetrics) {
		t.Errorf("expected %d metrics, got %d", len(expectedMetrics), len(actualMetrics))
	}

	for key, expected := range expectedMetrics {
		actual, ok := actualMetrics[key]
		if !ok {
			t.Errorf("expected metric %q not found", key)
			continue
		}

		if !reflect.DeepEqual(expected, actual) {
			t.Errorf("metric %q: expected %+v, got %+v", key, expected, actual)
		}
	}
}

func TestKVMResourceCapacityKPI_Collect_UtilizedMetric(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hostDetails, err := v1alpha1.BoxFeatureList([]any{
		&compute.HostDetails{
			ComputeHost:      "kvm-host",
			AvailabilityZone: "az2",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "qemu",
			HypervisorFamily: "kvm",
			WorkloadType:     "hana",
			Enabled:          false,
			Decommissioned:   false,
			ExternalCustomer: false,
			DisabledReason:   testlib.Ptr("test"),
			PinnedProjects:   testlib.Ptr("project1,project2"),
		},
		// Skip this because it's not a KVM host
		&compute.HostDetails{
			ComputeHost:      "vmware-host",
			AvailabilityZone: "az1",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "vcenter",
			HypervisorFamily: "vmware",
			WorkloadType:     "general-purpose",
			Enabled:          true,
			Decommissioned:   true,
			ExternalCustomer: true,
			DisabledReason:   nil,
			PinnedProjects:   nil,
		},
		// Skip this because placement doesn't report any capacity for this host
		&compute.HostDetails{
			ComputeHost:      "kvm-host-2",
			AvailabilityZone: "az2",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "qemu",
			HypervisorFamily: "kvm",
			WorkloadType:     "hana",
			Enabled:          false,
			Decommissioned:   false,
			ExternalCustomer: false,
			DisabledReason:   testlib.Ptr("test"),
			PinnedProjects:   testlib.Ptr("project1,project2"),
		},
		// Skip this because it's a ironic host
		&compute.HostDetails{
			ComputeHost:      "ironic-host",
			AvailabilityZone: "az2",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "ironic",
			HypervisorFamily: "vmware",
			WorkloadType:     "hana",
			Enabled:          false,
			Decommissioned:   false,
			ExternalCustomer: false,
			DisabledReason:   testlib.Ptr("test"),
			PinnedProjects:   testlib.Ptr("project1"),
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hostUtilizations, err := v1alpha1.BoxFeatureList([]any{
		&compute.HostUtilization{
			ComputeHost:            "vmware-host",
			TotalVCPUsAllocatable:  100,
			TotalRAMAllocatableMB:  200,
			TotalDiskAllocatableGB: 300,
			VCPUsUsed:              40,
			RAMUsedMB:              100,
			DiskUsedGB:             30,
		},
		&compute.HostUtilization{
			ComputeHost:            "kvm-host",
			TotalVCPUsAllocatable:  100,
			TotalRAMAllocatableMB:  100,
			TotalDiskAllocatableGB: 100,
			VCPUsUsed:              75,
			RAMUsedMB:              80,
			DiskUsedGB:             85,
		},
		&compute.HostUtilization{
			ComputeHost:            "ironic-host",
			TotalVCPUsAllocatable:  0,
			TotalRAMAllocatableMB:  0,
			TotalDiskAllocatableGB: 0,
			VCPUsUsed:              0,
			RAMUsedMB:              0,
			DiskUsedGB:             0,
		},
		// No Capacity reported for host kvm-host-2
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &KVMResourceCapacityKPI{}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(&v1alpha1.Knowledge{
			ObjectMeta: v1.ObjectMeta{Name: "host-details"},
			Status:     v1alpha1.KnowledgeStatus{Raw: hostDetails},
		}, &v1alpha1.Knowledge{
			ObjectMeta: v1.ObjectMeta{Name: "host-utilization"},
			Status:     v1alpha1.KnowledgeStatus{Raw: hostUtilizations},
		}).
		Build()
	if err := kpi.Init(nil, client, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan prometheus.Metric, 100)
	kpi.Collect(ch)
	close(ch)

	type HostResourceMetric struct {
		ComputeHost      string
		Resource         string
		AvailabilityZone string
		Enabled          string
		Decommissioned   string
		ExternalCustomer string
		CPUArchitecture  string
		WorkloadType     string
		Maintenance      string
		Value            float64
	}

	actualMetrics := make(map[string]HostResourceMetric, 0)

	for metric := range ch {
		desc := metric.Desc().String()
		metricName := getMetricName(desc)

		// Only consider cortex_kvm_host_capacity_utilized metric in this test
		if metricName != "cortex_kvm_host_capacity_utilized" {
			continue
		}

		var m prometheusgo.Metric
		if err := metric.Write(&m); err != nil {
			t.Fatalf("failed to write metric: %v", err)
		}

		labels := make(map[string]string)
		for _, label := range m.Label {
			labels[label.GetName()] = label.GetValue()
		}

		key := labels["compute_host"] + "-" + labels["resource"]

		actualMetrics[key] = HostResourceMetric{
			ComputeHost:      labels["compute_host"],
			Resource:         labels["resource"],
			AvailabilityZone: labels["availability_zone"],
			Enabled:          labels["enabled"],
			Decommissioned:   labels["decommissioned"],
			ExternalCustomer: labels["external_customer"],
			CPUArchitecture:  labels["cpu_architecture"],
			WorkloadType:     labels["workload_type"],
			Maintenance:      labels["maintenance"],
			Value:            m.GetGauge().GetValue(),
		}
	}

	expectedMetrics := map[string]HostResourceMetric{
		"kvm-host-cpu": {
			ComputeHost:      "kvm-host",
			Resource:         "cpu",
			AvailabilityZone: "az2",
			Enabled:          "false",
			Decommissioned:   "false",
			ExternalCustomer: "false",
			CPUArchitecture:  "cascade-lake",
			WorkloadType:     "hana",
			Maintenance:      "false",
			Value:            75,
		},
		"kvm-host-ram": {
			ComputeHost:      "kvm-host",
			Resource:         "ram",
			AvailabilityZone: "az2",
			Enabled:          "false",
			Decommissioned:   "false",
			ExternalCustomer: "false",
			Maintenance:      "false",
			CPUArchitecture:  "cascade-lake",
			WorkloadType:     "hana",
			Value:            80,
		},
		"kvm-host-disk": {
			ComputeHost:      "kvm-host",
			Resource:         "disk",
			AvailabilityZone: "az2",
			Enabled:          "false",
			Decommissioned:   "false",
			ExternalCustomer: "false",
			CPUArchitecture:  "cascade-lake",
			WorkloadType:     "hana",
			Maintenance:      "false",
			Value:            85,
		},
	}

	if len(expectedMetrics) != len(actualMetrics) {
		t.Errorf("expected %d metrics, got %d", len(expectedMetrics), len(actualMetrics))
	}

	for key, expected := range expectedMetrics {
		actual, ok := actualMetrics[key]
		if !ok {
			t.Errorf("expected metric %q not found", key)
			continue
		}

		if !reflect.DeepEqual(expected, actual) {
			t.Errorf("metric %q: expected %+v, got %+v", key, expected, actual)
		}
	}
}
