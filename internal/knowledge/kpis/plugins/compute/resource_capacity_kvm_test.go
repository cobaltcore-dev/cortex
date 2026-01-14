// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	prometheusgo "github.com/prometheus/client_model/go"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestKVMResourceCapacityKPI_Init(t *testing.T) {
	kpi := &KVMResourceCapacityKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

type metricLabels struct {
	ComputeHost      string
	Resource         string
	AvailabilityZone string
	BuildingBlock    string
	CPUArchitecture  string
	WorkloadType     string
	Enabled          string
	Decommissioned   string
	ExternalCustomer string
	Maintenance      string
}

type expectedMetric struct {
	Labels metricLabels
	Value  float64
}

func TestKVMResourceCapacityKPI_Collect(t *testing.T) {
	tests := []struct {
		name            string
		hypervisors     []hv1.Hypervisor
		expectedMetrics map[string][]expectedMetric // metric_name -> []expectedMetric
	}{
		{
			name: "single hypervisor with default traits",
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "node001-bb088",
						Labels: map[string]string{
							"topology.kubernetes.io/zone": "qa-de-1a",
						},
					},
					Status: hv1.HypervisorStatus{
						Capacity: map[string]resource.Quantity{
							"cpu":    resource.MustParse("128"),
							"memory": resource.MustParse("512Gi"),
						},
						Allocation: map[string]resource.Quantity{
							"cpu":    resource.MustParse("64"),
							"memory": resource.MustParse("256Gi"),
						},
						Traits: []string{},
					},
				},
			},
			expectedMetrics: map[string][]expectedMetric{
				"cortex_kvm_host_capacity_total": {
					{
						Labels: metricLabels{
							ComputeHost:      "node001-bb088",
							Resource:         "cpu",
							AvailabilityZone: "qa-de-1a",
							BuildingBlock:    "bb088",
							CPUArchitecture:  "cascade-lake",
							WorkloadType:     "general-purpose",
							Enabled:          "true",
							Decommissioned:   "false",
							ExternalCustomer: "false",
							Maintenance:      "false",
						},
						Value: 128,
					},
					{
						Labels: metricLabels{
							ComputeHost:      "node001-bb088",
							Resource:         "ram",
							AvailabilityZone: "qa-de-1a",
							BuildingBlock:    "bb088",
							CPUArchitecture:  "cascade-lake",
							WorkloadType:     "general-purpose",
							Enabled:          "true",
							Decommissioned:   "false",
							ExternalCustomer: "false",
							Maintenance:      "false",
						},
						Value: 549755813888, // 512Gi in bytes
					},
				},
				"cortex_kvm_host_capacity_utilized": {
					{
						Labels: metricLabels{
							ComputeHost:      "node001-bb088",
							Resource:         "cpu",
							AvailabilityZone: "qa-de-1a",
							BuildingBlock:    "bb088",
							CPUArchitecture:  "cascade-lake",
							WorkloadType:     "general-purpose",
							Enabled:          "true",
							Decommissioned:   "false",
							ExternalCustomer: "false",
							Maintenance:      "false",
						},
						Value: 64,
					},
					{
						Labels: metricLabels{
							ComputeHost:      "node001-bb088",
							Resource:         "ram",
							AvailabilityZone: "qa-de-1a",
							BuildingBlock:    "bb088",
							CPUArchitecture:  "cascade-lake",
							WorkloadType:     "general-purpose",
							Enabled:          "true",
							Decommissioned:   "false",
							ExternalCustomer: "false",
							Maintenance:      "false",
						},
						Value: 274877906944, // 256Gi in bytes
					},
				},
			},
		},
		{
			name: "hypervisor with sapphire rapids and hana traits",
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "node002-bb089",
						Labels: map[string]string{
							"topology.kubernetes.io/zone": "qa-de-1b",
						},
					},
					Status: hv1.HypervisorStatus{
						Capacity: map[string]resource.Quantity{
							"cpu":    resource.MustParse("256"),
							"memory": resource.MustParse("1Ti"),
						},
						Allocation: map[string]resource.Quantity{
							"cpu":    resource.MustParse("128"),
							"memory": resource.MustParse("512Gi"),
						},
						Traits: []string{
							"CUSTOM_HW_SAPPHIRE_RAPIDS",
							"CUSTOM_HANA_EXCLUSIVE_HOST",
						},
					},
				},
			},
			expectedMetrics: map[string][]expectedMetric{
				"cortex_kvm_host_capacity_total": {
					{
						Labels: metricLabels{
							ComputeHost:      "node002-bb089",
							Resource:         "cpu",
							AvailabilityZone: "qa-de-1b",
							BuildingBlock:    "bb089",
							CPUArchitecture:  "sapphire-rapids",
							WorkloadType:     "hana",
							Enabled:          "true",
							Decommissioned:   "false",
							ExternalCustomer: "false",
							Maintenance:      "false",
						},
						Value: 256,
					},
					{
						Labels: metricLabels{
							ComputeHost:      "node002-bb089",
							Resource:         "ram",
							AvailabilityZone: "qa-de-1b",
							BuildingBlock:    "bb089",
							CPUArchitecture:  "sapphire-rapids",
							WorkloadType:     "hana",
							Enabled:          "true",
							Decommissioned:   "false",
							ExternalCustomer: "false",
							Maintenance:      "false",
						},
						Value: 1099511627776, // 1Ti in bytes
					},
				},
			},
		},
		{
			name: "hypervisor with decommissioned and external customer traits",
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "node003-bb090",
						Labels: map[string]string{
							"topology.kubernetes.io/zone": "qa-de-1c",
						},
					},
					Status: hv1.HypervisorStatus{
						Capacity: map[string]resource.Quantity{
							"cpu":    resource.MustParse("64"),
							"memory": resource.MustParse("256Gi"),
						},
						Allocation: map[string]resource.Quantity{
							"cpu":    resource.MustParse("32"),
							"memory": resource.MustParse("128Gi"),
						},
						Traits: []string{
							"CUSTOM_DECOMMISSIONING",
							"CUSTOM_EXTERNAL_CUSTOMER_SUPPORTED",
						},
					},
				},
			},
			expectedMetrics: map[string][]expectedMetric{
				"cortex_kvm_host_capacity_total": {
					{
						Labels: metricLabels{
							ComputeHost:      "node003-bb090",
							Resource:         "cpu",
							AvailabilityZone: "qa-de-1c",
							BuildingBlock:    "bb090",
							CPUArchitecture:  "cascade-lake",
							WorkloadType:     "general-purpose",
							Enabled:          "true",
							Decommissioned:   "true",
							ExternalCustomer: "true",
							Maintenance:      "false",
						},
						Value: 64,
					},
				},
			},
		},
		{
			name: "multiple hypervisors",
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "node010-bb100",
						Labels: map[string]string{
							"topology.kubernetes.io/zone": "qa-de-1a",
						},
					},
					Status: hv1.HypervisorStatus{
						Capacity: map[string]resource.Quantity{
							"cpu":    resource.MustParse("100"),
							"memory": resource.MustParse("200Gi"),
						},
						Allocation: map[string]resource.Quantity{
							"cpu":    resource.MustParse("50"),
							"memory": resource.MustParse("100Gi"),
						},
						Traits: []string{},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "node020-bb200",
						Labels: map[string]string{
							"topology.kubernetes.io/zone": "qa-de-1b",
						},
					},
					Status: hv1.HypervisorStatus{
						Capacity: map[string]resource.Quantity{
							"cpu":    resource.MustParse("200"),
							"memory": resource.MustParse("400Gi"),
						},
						Allocation: map[string]resource.Quantity{
							"cpu":    resource.MustParse("150"),
							"memory": resource.MustParse("300Gi"),
						},
						Traits: []string{"CUSTOM_HW_SAPPHIRE_RAPIDS"},
					},
				},
			},
			expectedMetrics: map[string][]expectedMetric{
				"cortex_kvm_host_capacity_total": {
					{
						Labels: metricLabels{
							ComputeHost:      "node010-bb100",
							Resource:         "cpu",
							AvailabilityZone: "qa-de-1a",
							BuildingBlock:    "bb100",
							CPUArchitecture:  "cascade-lake",
							WorkloadType:     "general-purpose",
							Enabled:          "true",
							Decommissioned:   "false",
							ExternalCustomer: "false",
							Maintenance:      "false",
						},
						Value: 100,
					},
					{
						Labels: metricLabels{
							ComputeHost:      "node020-bb200",
							Resource:         "cpu",
							AvailabilityZone: "qa-de-1b",
							BuildingBlock:    "bb200",
							CPUArchitecture:  "sapphire-rapids",
							WorkloadType:     "general-purpose",
							Enabled:          "true",
							Decommissioned:   "false",
							ExternalCustomer: "false",
							Maintenance:      "false",
						},
						Value: 200,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := hv1.AddToScheme(scheme); err != nil {
				t.Fatalf("failed to add hypervisor scheme: %v", err)
			}

			objects := make([]runtime.Object, len(tt.hypervisors))
			for i := range tt.hypervisors {
				objects[i] = &tt.hypervisors[i]
			}

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objects...).
				Build()

			kpi := &KVMResourceCapacityKPI{}
			if err := kpi.Init(nil, client, conf.NewRawOpts("{}")); err != nil {
				t.Fatalf("failed to init KPI: %v", err)
			}

			ch := make(chan prometheus.Metric, 1000)
			kpi.Collect(ch)
			close(ch)

			actualMetrics := make(map[string][]expectedMetric)
			for metric := range ch {
				var m prometheusgo.Metric
				if err := metric.Write(&m); err != nil {
					t.Fatalf("failed to write metric: %v", err)
				}

				// Extract metric name from description
				desc := metric.Desc().String()
				metricName := getMetricName(desc)

				// Extract labels
				labels := metricLabels{}
				for _, label := range m.Label {
					switch label.GetName() {
					case "compute_host":
						labels.ComputeHost = label.GetValue()
					case "resource":
						labels.Resource = label.GetValue()
					case "availability_zone":
						labels.AvailabilityZone = label.GetValue()
					case "building_block":
						labels.BuildingBlock = label.GetValue()
					case "cpu_architecture":
						labels.CPUArchitecture = label.GetValue()
					case "workload_type":
						labels.WorkloadType = label.GetValue()
					case "enabled":
						labels.Enabled = label.GetValue()
					case "decommissioned":
						labels.Decommissioned = label.GetValue()
					case "external_customer":
						labels.ExternalCustomer = label.GetValue()
					case "maintenance":
						labels.Maintenance = label.GetValue()
					}
				}

				actualMetrics[metricName] = append(actualMetrics[metricName], expectedMetric{
					Labels: labels,
					Value:  m.GetGauge().GetValue(),
				})
			}

			// Verify expected metrics
			for metricName, expectedList := range tt.expectedMetrics {
				actualList, ok := actualMetrics[metricName]
				if !ok {
					t.Errorf("metric %q not found in actual metrics", metricName)
					continue
				}

				for _, expected := range expectedList {
					found := false
					for _, actual := range actualList {
						if actual.Labels == expected.Labels {
							found = true
							if actual.Value != expected.Value {
								t.Errorf("metric %q with labels %+v: expected value %f, got %f",
									metricName, expected.Labels, expected.Value, actual.Value)
							}
							break
						}
					}
					if !found {
						t.Errorf("metric %q with labels %+v not found in actual metrics",
							metricName, expected.Labels)
					}
				}
			}
		})
	}
}
