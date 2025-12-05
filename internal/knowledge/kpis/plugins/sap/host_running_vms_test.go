// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sap

import (
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/sap"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	testlib "github.com/cobaltcore-dev/cortex/pkg/testing"
	"github.com/prometheus/client_golang/prometheus"
	prometheusgo "github.com/prometheus/client_model/go"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHostRunningVMsKPI_Init(t *testing.T) {
	kpi := &HostRunningVMsKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestHostRunningVMsKPI_Collect(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hypervisors, err := v1alpha1.BoxFeatureList([]any{
		&sap.HostDetails{
			ComputeHost:      "host1",
			AvailabilityZone: "az1",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "vcenter",
			HypervisorFamily: "vmware",
			RunningVMs:       5,
			WorkloadType:     "general-purpose",
			Enabled:          true,
			Decommissioned:   true,
			ExternalCustomer: true,
			PinnedProjects:   testlib.Ptr("project-123,project-456"),
		},
		// Should be ignored since its an ironic host
		&sap.HostDetails{
			ComputeHost:      "host2",
			AvailabilityZone: "az1",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "ironic",
			HypervisorFamily: "vmware",
			RunningVMs:       5,
			WorkloadType:     "general-purpose",
			Enabled:          true,
		},
		// Should be ignored since it has no usage data
		&sap.HostDetails{
			ComputeHost:      "host3",
			AvailabilityZone: "az1",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "ironic",
			HypervisorFamily: "vmware",
			RunningVMs:       5,
			WorkloadType:     "general-purpose",
			Enabled:          true,
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hostUtilizations, err := v1alpha1.BoxFeatureList([]any{
		&shared.HostUtilization{
			ComputeHost:            "host1",
			TotalVCPUsAllocatable:  100,
			TotalRAMAllocatableMB:  200,
			TotalDiskAllocatableGB: 300,
		},
		// Ironic host
		&shared.HostUtilization{
			ComputeHost:            "host2",
			TotalVCPUsAllocatable:  1,
			TotalRAMAllocatableMB:  1,
			TotalDiskAllocatableGB: 1,
		},
		// No Capacity reported for host3
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &HostRunningVMsKPI{}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(&v1alpha1.Knowledge{
			ObjectMeta: v1.ObjectMeta{Name: "sap-host-details"},
			Status:     v1alpha1.KnowledgeStatus{Raw: hypervisors},
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

	type HostRunningVMsMetric struct {
		ComputeHost      string
		AvailabilityZone string
		Enabled          string
		Decommissioned   string
		ExternalCustomer string
		CPUArchitecture  string
		WorkloadType     string
		HypervisorFamily string
		PinnedProjects   string
		Value            float64
	}

	actualMetrics := make(map[string]HostRunningVMsMetric, 0)

	for metric := range ch {
		var m prometheusgo.Metric
		if err := metric.Write(&m); err != nil {
			t.Fatalf("failed to write metric: %v", err)
		}

		labels := make(map[string]string)
		for _, label := range m.Label {
			labels[label.GetName()] = label.GetValue()
		}

		key := labels["compute_host"]

		actualMetrics[key] = HostRunningVMsMetric{
			ComputeHost:      labels["compute_host"],
			AvailabilityZone: labels["availability_zone"],
			Enabled:          labels["enabled"],
			Decommissioned:   labels["decommissioned"],
			ExternalCustomer: labels["external_customer"],
			CPUArchitecture:  labels["cpu_architecture"],
			WorkloadType:     labels["workload_type"],
			HypervisorFamily: labels["hypervisor_family"],
			PinnedProjects:   labels["pinned_projects"],
			Value:            m.GetGauge().GetValue(),
		}
	}

	expectedMetrics := map[string]HostRunningVMsMetric{
		"host1": {
			ComputeHost:      "host1",
			AvailabilityZone: "az1",
			Enabled:          "true",
			Decommissioned:   "true",
			ExternalCustomer: "true",
			CPUArchitecture:  "cascade-lake",
			WorkloadType:     "general-purpose",
			HypervisorFamily: "vmware",
			Value:            5,
			PinnedProjects:   "project-123,project-456",
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
