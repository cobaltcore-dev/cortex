// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
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

type kvmMetricLabels struct {
	ComputeHost      string
	Resource         string
	Type             string
	AvailabilityZone string
	BuildingBlock    string
	CPUArchitecture  string
	WorkloadType     string
	Enabled          string
	Decommissioned   string
	ExternalCustomer string
	Maintenance      string
}

type kvmExpectedMetric struct {
	Name   string // metric family name (e.g. "cortex_kvm_host_capacity_total")
	Labels kvmMetricLabels
	Value  float64
}

func defaultHostLabels(host, az, bb string) kvmMetricLabels {
	return kvmMetricLabels{
		ComputeHost:      host,
		AvailabilityZone: az,
		BuildingBlock:    bb,
		CPUArchitecture:  "cascade-lake",
		WorkloadType:     "general-purpose",
		Enabled:          "true",
		Decommissioned:   "false",
		ExternalCustomer: "false",
		Maintenance:      "false",
	}
}

func totalMetric(host, res, az, bb string, value float64) kvmExpectedMetric {
	l := defaultHostLabels(host, az, bb)
	l.Resource = res
	return kvmExpectedMetric{Name: "cortex_kvm_host_capacity_total", Labels: l, Value: value}
}

func usageMetric(host, res, capacityType, az, bb string, value float64) kvmExpectedMetric {
	l := defaultHostLabels(host, az, bb)
	l.Resource = res
	l.Type = capacityType
	return kvmExpectedMetric{Name: "cortex_kvm_host_capacity_usage", Labels: l, Value: value}
}

func TestKVMResourceCapacityKPI_Collect(t *testing.T) {
	tests := []struct {
		name            string
		hypervisors     []hv1.Hypervisor
		reservations    []v1alpha1.Reservation
		expectedMetrics []kvmExpectedMetric
	}{
		{
			name: "single hypervisor with nil effective capacity",
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "node001-bb088",
						Labels: map[string]string{
							"topology.kubernetes.io/zone": "qa-1a",
						},
					},
					Status: hv1.HypervisorStatus{
						EffectiveCapacity: nil,
						Allocation: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("64"),
							hv1.ResourceMemory: resource.MustParse("256Gi"),
						},
						Traits: []string{},
					},
				},
			},
			expectedMetrics: []kvmExpectedMetric{},
		},
		{
			name: "single hypervisor with zero total capacity",
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "node001-bb088",
						Labels: map[string]string{
							"topology.kubernetes.io/zone": "qa-1a",
						},
					},
					Status: hv1.HypervisorStatus{
						EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("0"),
							hv1.ResourceMemory: resource.MustParse("0"),
						},
						Allocation: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("0"),
							hv1.ResourceMemory: resource.MustParse("0"),
						},
						Traits: []string{},
					},
				},
			},
			expectedMetrics: []kvmExpectedMetric{},
		},
		{
			name: "single hypervisor with default traits, no reservations",
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "node001-bb088",
						Labels: map[string]string{
							"topology.kubernetes.io/zone": "qa-1a",
						},
					},
					Status: hv1.HypervisorStatus{
						EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("128"),
							hv1.ResourceMemory: resource.MustParse("512Gi"),
						},
						Allocation: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("64"),
							hv1.ResourceMemory: resource.MustParse("256Gi"),
						},
						Traits: []string{},
					},
				},
			},
			expectedMetrics: []kvmExpectedMetric{
				totalMetric("node001-bb088", "cpu", "qa-1a", "bb088", 128),
				totalMetric("node001-bb088", "ram", "qa-1a", "bb088", 549755813888),
				usageMetric("node001-bb088", "cpu", "utilized", "qa-1a", "bb088", 64),
				usageMetric("node001-bb088", "ram", "utilized", "qa-1a", "bb088", 274877906944),
				usageMetric("node001-bb088", "cpu", "reserved", "qa-1a", "bb088", 0),
				usageMetric("node001-bb088", "ram", "reserved", "qa-1a", "bb088", 0),
				usageMetric("node001-bb088", "cpu", "failover", "qa-1a", "bb088", 0),
				usageMetric("node001-bb088", "ram", "failover", "qa-1a", "bb088", 0),
				usageMetric("node001-bb088", "cpu", "payg", "qa-1a", "bb088", 64),           // 128-64-0-0
				usageMetric("node001-bb088", "ram", "payg", "qa-1a", "bb088", 274877906944), // 512Gi-256Gi
			},
		},
		{
			name: "hypervisor with sapphire rapids and hana traits",
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "node002-bb089",
						Labels: map[string]string{
							"topology.kubernetes.io/zone": "qa-1b",
						},
					},
					Status: hv1.HypervisorStatus{
						EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("256"),
							hv1.ResourceMemory: resource.MustParse("1Ti"),
						},
						Allocation: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("128"),
							hv1.ResourceMemory: resource.MustParse("512Gi"),
						},
						Traits: []string{
							"CUSTOM_HW_SAPPHIRE_RAPIDS",
							"CUSTOM_HANA_EXCLUSIVE_HOST",
						},
					},
				},
			},
			expectedMetrics: []kvmExpectedMetric{
				{
					Name: "cortex_kvm_host_capacity_total",
					Labels: kvmMetricLabels{
						ComputeHost: "node002-bb089", Resource: "cpu",
						AvailabilityZone: "qa-1b", BuildingBlock: "bb089",
						CPUArchitecture: "sapphire-rapids", WorkloadType: "hana",
						Enabled: "true", Decommissioned: "false", ExternalCustomer: "false", Maintenance: "false",
					},
					Value: 256,
				},
				{
					Name: "cortex_kvm_host_capacity_total",
					Labels: kvmMetricLabels{
						ComputeHost: "node002-bb089", Resource: "ram",
						AvailabilityZone: "qa-1b", BuildingBlock: "bb089",
						CPUArchitecture: "sapphire-rapids", WorkloadType: "hana",
						Enabled: "true", Decommissioned: "false", ExternalCustomer: "false", Maintenance: "false",
					},
					Value: 1099511627776,
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
							"topology.kubernetes.io/zone": "qa-1c",
						},
					},
					Status: hv1.HypervisorStatus{
						EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("64"),
							hv1.ResourceMemory: resource.MustParse("256Gi"),
						},
						Allocation: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("32"),
							hv1.ResourceMemory: resource.MustParse("128Gi"),
						},
						Traits: []string{
							"CUSTOM_DECOMMISSIONING",
							"CUSTOM_EXTERNAL_CUSTOMER_SUPPORTED",
						},
					},
				},
			},
			expectedMetrics: []kvmExpectedMetric{
				{
					Name: "cortex_kvm_host_capacity_total",
					Labels: kvmMetricLabels{
						ComputeHost: "node003-bb090", Resource: "cpu",
						AvailabilityZone: "qa-1c", BuildingBlock: "bb090",
						CPUArchitecture: "cascade-lake", WorkloadType: "general-purpose",
						Enabled: "true", Decommissioned: "true", ExternalCustomer: "true", Maintenance: "false",
					},
					Value: 64,
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
							"topology.kubernetes.io/zone": "qa-1a",
						},
					},
					Status: hv1.HypervisorStatus{
						EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("100"),
							hv1.ResourceMemory: resource.MustParse("200Gi"),
						},
						Allocation: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("50"),
							hv1.ResourceMemory: resource.MustParse("100Gi"),
						},
						Traits: []string{},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "node020-bb200",
						Labels: map[string]string{
							"topology.kubernetes.io/zone": "qa-1b",
						},
					},
					Status: hv1.HypervisorStatus{
						EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("200"),
							hv1.ResourceMemory: resource.MustParse("400Gi"),
						},
						Allocation: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("150"),
							hv1.ResourceMemory: resource.MustParse("300Gi"),
						},
						Traits: []string{"CUSTOM_HW_SAPPHIRE_RAPIDS"},
					},
				},
			},
			expectedMetrics: []kvmExpectedMetric{
				totalMetric("node010-bb100", "cpu", "qa-1a", "bb100", 100),
				{
					Name: "cortex_kvm_host_capacity_total",
					Labels: kvmMetricLabels{
						ComputeHost: "node020-bb200", Resource: "cpu",
						AvailabilityZone: "qa-1b", BuildingBlock: "bb200",
						CPUArchitecture: "sapphire-rapids", WorkloadType: "general-purpose",
						Enabled: "true", Decommissioned: "false", ExternalCustomer: "false", Maintenance: "false",
					},
					Value: 200,
				},
			},
		},
		{
			name: "hypervisor with missing allocation data",
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "node004-bb091",
						Labels: map[string]string{
							"topology.kubernetes.io/zone": "qa-1d",
						},
					},
					Status: hv1.HypervisorStatus{
						EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("96"),
							hv1.ResourceMemory: resource.MustParse("384Gi"),
						},
						Allocation: nil,
						Traits:     []string{},
					},
				},
			},
			expectedMetrics: []kvmExpectedMetric{
				totalMetric("node004-bb091", "cpu", "qa-1d", "bb091", 96),
				totalMetric("node004-bb091", "ram", "qa-1d", "bb091", 412316860416),
				usageMetric("node004-bb091", "cpu", "utilized", "qa-1d", "bb091", 0),
				usageMetric("node004-bb091", "ram", "utilized", "qa-1d", "bb091", 0),
			},
		},
		{
			name: "failover reservation on a hypervisor",
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "node001-bb088",
						Labels: map[string]string{
							"topology.kubernetes.io/zone": "qa-1a",
						},
					},
					Status: hv1.HypervisorStatus{
						EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("128"),
							hv1.ResourceMemory: resource.MustParse("512Gi"),
						},
						Allocation: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("64"),
							hv1.ResourceMemory: resource.MustParse("256Gi"),
						},
						Traits: []string{},
					},
				},
			},
			reservations: []v1alpha1.Reservation{
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "failover-1",
					},
					Spec: v1alpha1.ReservationSpec{
						Type:             v1alpha1.ReservationTypeFailover,
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						Resources: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("16"),
							hv1.ResourceMemory: resource.MustParse("64Gi"),
						},
						FailoverReservation: &v1alpha1.FailoverReservationSpec{},
					},
					Status: v1alpha1.ReservationStatus{
						Host: "node001-bb088",
						Conditions: []v1.Condition{
							{Type: v1alpha1.ReservationConditionReady, Status: v1.ConditionTrue},
						},
					},
				},
			},
			expectedMetrics: []kvmExpectedMetric{
				totalMetric("node001-bb088", "cpu", "qa-1a", "bb088", 128),
				totalMetric("node001-bb088", "ram", "qa-1a", "bb088", 549755813888),
				usageMetric("node001-bb088", "cpu", "utilized", "qa-1a", "bb088", 64),
				usageMetric("node001-bb088", "ram", "utilized", "qa-1a", "bb088", 274877906944),
				usageMetric("node001-bb088", "cpu", "reserved", "qa-1a", "bb088", 0),
				usageMetric("node001-bb088", "ram", "reserved", "qa-1a", "bb088", 0),
				usageMetric("node001-bb088", "cpu", "failover", "qa-1a", "bb088", 16),
				usageMetric("node001-bb088", "ram", "failover", "qa-1a", "bb088", 68719476736), // 64Gi
				usageMetric("node001-bb088", "cpu", "payg", "qa-1a", "bb088", 48),              // 128-64-0-16
				usageMetric("node001-bb088", "ram", "payg", "qa-1a", "bb088", 206158430208),    // 512Gi-256Gi-0-64Gi = 192Gi
			},
		},
		{
			name: "committed resource reservation with partial allocation",
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "node001-bb088",
						Labels: map[string]string{
							"topology.kubernetes.io/zone": "qa-1a",
						},
					},
					Status: hv1.HypervisorStatus{
						EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("128"),
							hv1.ResourceMemory: resource.MustParse("512Gi"),
						},
						Allocation: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("64"),
							hv1.ResourceMemory: resource.MustParse("256Gi"),
						},
						Traits: []string{},
					},
				},
			},
			reservations: []v1alpha1.Reservation{
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "committed-1",
					},
					Spec: v1alpha1.ReservationSpec{
						Type:             v1alpha1.ReservationTypeCommittedResource,
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						Resources: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("32"),
							hv1.ResourceMemory: resource.MustParse("128Gi"),
						},
						CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
							Allocations: map[string]v1alpha1.CommittedResourceAllocation{
								"vm-uuid-1": {
									Resources: map[hv1.ResourceName]resource.Quantity{
										hv1.ResourceCPU:    resource.MustParse("8"),
										hv1.ResourceMemory: resource.MustParse("32Gi"),
									},
								},
							},
						},
					},
					Status: v1alpha1.ReservationStatus{
						Host: "node001-bb088",
						Conditions: []v1.Condition{
							{Type: v1alpha1.ReservationConditionReady, Status: v1.ConditionTrue},
						},
					},
				},
			},
			expectedMetrics: []kvmExpectedMetric{
				// reserved = 32-8=24 CPU, 128Gi-32Gi=96Gi RAM (not in use)
				usageMetric("node001-bb088", "cpu", "reserved", "qa-1a", "bb088", 24),
				usageMetric("node001-bb088", "ram", "reserved", "qa-1a", "bb088", 103079215104), // 96Gi
				usageMetric("node001-bb088", "cpu", "failover", "qa-1a", "bb088", 0),
				usageMetric("node001-bb088", "ram", "failover", "qa-1a", "bb088", 0),
				usageMetric("node001-bb088", "cpu", "payg", "qa-1a", "bb088", 40),           // 128-64-24-0
				usageMetric("node001-bb088", "ram", "payg", "qa-1a", "bb088", 171798691840), // 512Gi-256Gi-96Gi-0 = 160Gi
			},
		},
		{
			name: "non-ready reservation should be ignored",
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "node001-bb088",
						Labels: map[string]string{
							"topology.kubernetes.io/zone": "qa-1a",
						},
					},
					Status: hv1.HypervisorStatus{
						EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("128"),
							hv1.ResourceMemory: resource.MustParse("512Gi"),
						},
						Allocation: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("64"),
							hv1.ResourceMemory: resource.MustParse("256Gi"),
						},
						Traits: []string{},
					},
				},
			},
			reservations: []v1alpha1.Reservation{
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "failover-not-ready",
					},
					Spec: v1alpha1.ReservationSpec{
						Type:             v1alpha1.ReservationTypeFailover,
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						Resources: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("16"),
							hv1.ResourceMemory: resource.MustParse("64Gi"),
						},
						FailoverReservation: &v1alpha1.FailoverReservationSpec{},
					},
					Status: v1alpha1.ReservationStatus{
						Host: "node001-bb088",
						Conditions: []v1.Condition{
							{Type: v1alpha1.ReservationConditionReady, Status: v1.ConditionFalse},
						},
					},
				},
			},
			expectedMetrics: []kvmExpectedMetric{
				// Non-ready reservation ignored, so failover = 0
				usageMetric("node001-bb088", "cpu", "failover", "qa-1a", "bb088", 0),
				usageMetric("node001-bb088", "ram", "failover", "qa-1a", "bb088", 0),
				usageMetric("node001-bb088", "cpu", "payg", "qa-1a", "bb088", 64),
				usageMetric("node001-bb088", "ram", "payg", "qa-1a", "bb088", 274877906944),
			},
		},
		{
			name: "multiple failover reservations on same host are summed",
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "node001-bb088",
						Labels: map[string]string{
							"topology.kubernetes.io/zone": "qa-1a",
						},
					},
					Status: hv1.HypervisorStatus{
						EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("128"),
							hv1.ResourceMemory: resource.MustParse("512Gi"),
						},
						Allocation: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("64"),
							hv1.ResourceMemory: resource.MustParse("256Gi"),
						},
						Traits: []string{},
					},
				},
			},
			reservations: []v1alpha1.Reservation{
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "failover-1",
					},
					Spec: v1alpha1.ReservationSpec{
						Type:             v1alpha1.ReservationTypeFailover,
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						Resources: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("8"),
							hv1.ResourceMemory: resource.MustParse("32Gi"),
						},
						FailoverReservation: &v1alpha1.FailoverReservationSpec{},
					},
					Status: v1alpha1.ReservationStatus{
						Host: "node001-bb088",
						Conditions: []v1.Condition{
							{Type: v1alpha1.ReservationConditionReady, Status: v1.ConditionTrue},
						},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "failover-2",
					},
					Spec: v1alpha1.ReservationSpec{
						Type:             v1alpha1.ReservationTypeFailover,
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						Resources: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("12"),
							hv1.ResourceMemory: resource.MustParse("48Gi"),
						},
						FailoverReservation: &v1alpha1.FailoverReservationSpec{},
					},
					Status: v1alpha1.ReservationStatus{
						Host: "node001-bb088",
						Conditions: []v1.Condition{
							{Type: v1alpha1.ReservationConditionReady, Status: v1.ConditionTrue},
						},
					},
				},
			},
			expectedMetrics: []kvmExpectedMetric{
				// failover = 8+12=20 CPU, 32Gi+48Gi=80Gi RAM
				usageMetric("node001-bb088", "cpu", "failover", "qa-1a", "bb088", 20),
				usageMetric("node001-bb088", "ram", "failover", "qa-1a", "bb088", 85899345920), // 80Gi
				usageMetric("node001-bb088", "cpu", "payg", "qa-1a", "bb088", 44),              // 128-64-0-20
				usageMetric("node001-bb088", "ram", "payg", "qa-1a", "bb088", 188978561024),    // 512Gi-256Gi-0-80Gi = 176Gi
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := hv1.AddToScheme(scheme); err != nil {
				t.Fatalf("failed to add hypervisor scheme: %v", err)
			}
			if err := v1alpha1.AddToScheme(scheme); err != nil {
				t.Fatalf("failed to add v1alpha1 scheme: %v", err)
			}

			objects := make([]runtime.Object, 0, len(tt.hypervisors)+len(tt.reservations))
			for i := range tt.hypervisors {
				objects = append(objects, &tt.hypervisors[i])
			}
			for i := range tt.reservations {
				objects = append(objects, &tt.reservations[i])
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

			var actualMetrics []kvmExpectedMetric
			for metric := range ch {
				var m prometheusgo.Metric
				if err := metric.Write(&m); err != nil {
					t.Fatalf("failed to write metric: %v", err)
				}

				metricName := getMetricName(metric.Desc().String())

				labels := kvmMetricLabels{}
				for _, label := range m.Label {
					switch label.GetName() {
					case "compute_host":
						labels.ComputeHost = label.GetValue()
					case "resource":
						labels.Resource = label.GetValue()
					case "type":
						labels.Type = label.GetValue()
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

				actualMetrics = append(actualMetrics, kvmExpectedMetric{
					Name:   metricName,
					Labels: labels,
					Value:  m.GetGauge().GetValue(),
				})
			}

			// Verify each expected metric is present with the correct value and metric name.
			for _, expected := range tt.expectedMetrics {
				found := false
				for _, actual := range actualMetrics {
					nameMatch := expected.Name == "" || actual.Name == expected.Name
					if nameMatch && actual.Labels == expected.Labels {
						found = true
						if actual.Value != expected.Value {
							t.Errorf("metric %s with labels %+v: expected value %f, got %f",
								expected.Name, expected.Labels, expected.Value, actual.Value)
						}
						break
					}
				}
				if !found {
					t.Errorf("metric %s with labels %+v not found in actual metrics",
						expected.Name, expected.Labels)
				}
			}
		})
	}
}

func TestAggregateReservationsByHost(t *testing.T) {
	tests := []struct {
		name                      string
		reservations              []v1alpha1.Reservation
		expectedFailover          map[string]hostReservationResources
		expectedCommittedNotInUse map[string]hostReservationResources
	}{
		{
			name:                      "empty reservations",
			reservations:              nil,
			expectedFailover:          map[string]hostReservationResources{},
			expectedCommittedNotInUse: map[string]hostReservationResources{},
		},
		{
			name: "reservation with no ready condition is skipped",
			reservations: []v1alpha1.Reservation{
				{
					Spec: v1alpha1.ReservationSpec{
						Type:             v1alpha1.ReservationTypeFailover,
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						Resources: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU: resource.MustParse("10"),
						},
					},
					Status: v1alpha1.ReservationStatus{
						Host: "host-1",
						// No conditions
					},
				},
			},
			expectedFailover:          map[string]hostReservationResources{},
			expectedCommittedNotInUse: map[string]hostReservationResources{},
		},
		{
			name: "reservation with empty host is skipped",
			reservations: []v1alpha1.Reservation{
				{
					Spec: v1alpha1.ReservationSpec{
						Type:             v1alpha1.ReservationTypeFailover,
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						Resources: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU: resource.MustParse("10"),
						},
					},
					Status: v1alpha1.ReservationStatus{
						Host: "",
						Conditions: []v1.Condition{
							{Type: v1alpha1.ReservationConditionReady, Status: v1.ConditionTrue},
						},
					},
				},
			},
			expectedFailover:          map[string]hostReservationResources{},
			expectedCommittedNotInUse: map[string]hostReservationResources{},
		},
		{
			name: "committed resource with nil spec does not panic",
			reservations: []v1alpha1.Reservation{
				{
					Spec: v1alpha1.ReservationSpec{
						Type:             v1alpha1.ReservationTypeCommittedResource,
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						Resources: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceCPU:    resource.MustParse("16"),
							hv1.ResourceMemory: resource.MustParse("64Gi"),
						},
						CommittedResourceReservation: nil,
					},
					Status: v1alpha1.ReservationStatus{
						Host: "host-1",
						Conditions: []v1.Condition{
							{Type: v1alpha1.ReservationConditionReady, Status: v1.ConditionTrue},
						},
					},
				},
			},
			expectedFailover: map[string]hostReservationResources{},
			expectedCommittedNotInUse: map[string]hostReservationResources{
				"host-1": {
					cpu:    resource.MustParse("16"),
					memory: resource.MustParse("64Gi"),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			failover, committed := aggregateReservationsByHost(tt.reservations)

			if len(failover) != len(tt.expectedFailover) {
				t.Errorf("failover map length: expected %d, got %d", len(tt.expectedFailover), len(failover))
			}
			for host, expected := range tt.expectedFailover {
				actual, ok := failover[host]
				if !ok {
					t.Errorf("failover: host %q not found", host)
					continue
				}
				if actual.cpu.Cmp(expected.cpu) != 0 {
					t.Errorf("failover[%s].cpu: expected %s, got %s", host, expected.cpu.String(), actual.cpu.String())
				}
				if actual.memory.Cmp(expected.memory) != 0 {
					t.Errorf("failover[%s].memory: expected %s, got %s", host, expected.memory.String(), actual.memory.String())
				}
			}

			if len(committed) != len(tt.expectedCommittedNotInUse) {
				t.Errorf("committed map length: expected %d, got %d", len(tt.expectedCommittedNotInUse), len(committed))
			}
			for host, expected := range tt.expectedCommittedNotInUse {
				actual, ok := committed[host]
				if !ok {
					t.Errorf("committed: host %q not found", host)
					continue
				}
				if actual.cpu.Cmp(expected.cpu) != 0 {
					t.Errorf("committed[%s].cpu: expected %s, got %s", host, expected.cpu.String(), actual.cpu.String())
				}
				if actual.memory.Cmp(expected.memory) != 0 {
					t.Errorf("committed[%s].memory: expected %s, got %s", host, expected.memory.String(), actual.memory.String())
				}
			}
		})
	}
}
