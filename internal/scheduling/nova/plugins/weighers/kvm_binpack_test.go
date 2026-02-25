// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"log/slog"
	"strings"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newHypervisor(name, capacityCPU, capacityMem, allocationCPU, allocationMem string) *hv1.Hypervisor {
	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: hv1.HypervisorStatus{
			Capacity: map[string]resource.Quantity{
				"cpu":    resource.MustParse(capacityCPU),
				"memory": resource.MustParse(capacityMem),
			},
			Allocation: map[string]resource.Quantity{
				"cpu":    resource.MustParse(allocationCPU),
				"memory": resource.MustParse(allocationMem),
			},
		},
	}
}

func newBinpackRequest(memoryMB, vcpus, numInstances uint64, hosts []string) api.ExternalSchedulerRequest {
	hostList := make([]api.ExternalSchedulerHost, len(hosts))
	for i, h := range hosts {
		hostList[i] = api.ExternalSchedulerHost{ComputeHost: h}
	}

	extraSpecs := map[string]string{
		"capabilities:hypervisor_type": "qemu",
	}

	spec := api.NovaSpec{
		ProjectID:    "project-A",
		InstanceUUID: "instance-123",
		NumInstances: numInstances,
		Flavor: api.NovaObject[api.NovaFlavor]{
			Data: api.NovaFlavor{
				Name:       "m1.large",
				VCPUs:      vcpus,
				MemoryMB:   memoryMB,
				ExtraSpecs: extraSpecs,
			},
		},
	}

	weights := make(map[string]float64)
	for _, h := range hosts {
		weights[h] = 1.0
	}

	return api.ExternalSchedulerRequest{
		Spec:    api.NovaObject[api.NovaSpec]{Data: spec},
		Hosts:   hostList,
		Weights: weights,
	}
}

func TestKVMBinpackStepOpts_Validate(t *testing.T) {
	tests := []struct {
		name    string
		opts    KVMBinpackStepOpts
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid opts with memory and cpu weights",
			opts: KVMBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 1.0,
					corev1.ResourceCPU:    1.0,
				},
			},
			wantErr: false,
		},
		{
			name: "inverted weights for worst-fit algorithm",
			opts: KVMBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: -1.0,
					corev1.ResourceCPU:    -1.0,
				},
			},
			wantErr: false,
		},
		{
			name: "valid opts with only memory weight",
			opts: KVMBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 2.0,
				},
			},
			wantErr: false,
		},
		{
			name: "valid opts with only cpu weight",
			opts: KVMBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceCPU: 0.5,
				},
			},
			wantErr: false,
		},
		{
			name: "zero weights should raise error",
			opts: KVMBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 0.0,
					corev1.ResourceCPU:    0.0,
				},
			},
			wantErr: true,
		},
		{
			name: "valid opts with empty resource weights",
			opts: KVMBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{},
			},
			wantErr: true,
		},
		{
			name:    "valid opts with nil resource weights",
			opts:    KVMBinpackStepOpts{},
			wantErr: true,
		},
		{
			name: "invalid opts with unsupported resource",
			opts: KVMBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceStorage: 1.0,
				},
			},
			wantErr: true,
			errMsg:  "unsupported resource",
		},
		{
			name: "invalid opts with unsupported ephemeral-storage resource",
			opts: KVMBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceEphemeralStorage: 1.0,
				},
			},
			wantErr: true,
			errMsg:  "unsupported resource",
		},
		{
			name: "invalid opts with custom unsupported resource",
			opts: KVMBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					"nvidia.com/gpu": 1.0,
				},
			},
			wantErr: true,
			errMsg:  "unsupported resource",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

func TestKVMBinpackStep_Run(t *testing.T) {
	scheme := buildTestScheme(t)

	tests := []struct {
		name            string
		hypervisors     []*hv1.Hypervisor
		request         api.ExternalSchedulerRequest
		opts            KVMBinpackStepOpts
		expectedWeights map[string]float64
		wantErr         bool
	}{
		{
			name: "basic binpacking with memory weight only",
			hypervisors: []*hv1.Hypervisor{
				// host1: capacity 100Gi, allocation (free) 80Gi -> used 20Gi, adding 8Gi VM -> 28Gi used
				// utilization after VM = 28/100 = 0.28
				newHypervisor("host1", "100", "100Gi", "80", "80Gi"),
				// host2: capacity 100Gi, allocation (free) 20Gi -> used 80Gi, adding 8Gi VM -> 88Gi used
				// utilization after VM = 88/100 = 0.88
				newHypervisor("host2", "100", "100Gi", "20", "20Gi"),
			},
			request: newBinpackRequest(8192, 4, 1, []string{"host1", "host2"}), // 8Gi memory
			opts: KVMBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 1.0,
				},
			},
			expectedWeights: map[string]float64{ // with 0.1 tolerance
				"host1": 0.3,
				"host2": 0.9,
			},
			wantErr: false,
		},
		{
			name: "basic binpacking with cpu weight only",
			hypervisors: []*hv1.Hypervisor{
				// host1: capacity 100 CPUs, allocation (free) 80 CPUs -> used 20 CPUs
				newHypervisor("host1", "100", "100Gi", "80", "80Gi"),
				// host2: capacity 100 CPUs, allocation (free) 20 CPUs -> used 80 CPUs
				newHypervisor("host2", "100", "100Gi", "20", "20Gi"),
			},
			request: newBinpackRequest(8192, 4, 1, []string{"host1", "host2"}),
			opts: KVMBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceCPU: 1.0,
				},
			},
			expectedWeights: map[string]float64{ // with 0.1 tolerance
				"host1": 0.3,
				"host2": 0.8,
			},
			wantErr: false,
		},
		{
			name: "binpacking with both cpu and memory weights",
			hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", "100", "100Gi", "80", "80Gi"),
				newHypervisor("host2", "100", "100Gi", "20", "20Gi"),
			},
			request: newBinpackRequest(8192, 4, 1, []string{"host1", "host2"}),
			opts: KVMBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceCPU:    1.0,
					corev1.ResourceMemory: 1.0,
				},
			},
			expectedWeights: map[string]float64{ // with 0.1 tolerance
				"host1": 0.26,
				"host2": 0.86,
			},
			wantErr: false,
		},
		{
			name: "binpacking with different weights for cpu and memory",
			hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", "100", "100Gi", "80", "80Gi"),
			},
			request: newBinpackRequest(8192, 4, 1, []string{"host1"}),
			opts: KVMBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceCPU:    2.0,
					corev1.ResourceMemory: 1.0,
				},
			},
			expectedWeights: map[string]float64{ // with 0.1 tolerance
				"host1": 0.25,
			},
			wantErr: false,
		},
		{
			name: "binpacking with multiple instances",
			hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", "100", "100Gi", "80", "80Gi"),
			},
			request: newBinpackRequest(8192, 4, 2, []string{"host1"}), // 2 instances
			opts: KVMBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceCPU: 1.0,
				},
			},
			expectedWeights: map[string]float64{ // with 0.1 tolerance
				"host1": 0.3,
			},
			wantErr: false,
		},
		{
			name:        "no hypervisors found - hosts skipped",
			hypervisors: []*hv1.Hypervisor{},
			request:     newBinpackRequest(8192, 4, 1, []string{"host1", "host2"}),
			opts: KVMBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceCPU: 1.0,
				},
			},
			expectedWeights: map[string]float64{
				// Both hosts should have default weight (0) since no hypervisors found
				"host1": 0,
				"host2": 0,
			},
			wantErr: false,
		},
		{
			name: "hypervisor missing for one host",
			hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", "100", "100Gi", "80", "80Gi"),
				// host2 hypervisor is missing
			},
			request: newBinpackRequest(8192, 4, 1, []string{"host1", "host2"}),
			opts: KVMBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceCPU: 1.0,
				},
			},
			expectedWeights: map[string]float64{
				"host1": 0.24,
				"host2": 0, // Default weight since no hypervisor
			},
			wantErr: false,
		},
		{
			name: "empty resource weights - no scoring",
			hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", "100", "100Gi", "80", "80Gi"),
			},
			request: newBinpackRequest(8192, 4, 1, []string{"host1"}),
			opts: KVMBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{},
			},
			expectedWeights: map[string]float64{
				"host1": 0, // No weights configured, score is 0
			},
			wantErr: false,
		},
		{
			name: "hypervisor with zero capacity - skipped",
			hypervisors: []*hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capacity: map[string]resource.Quantity{
							"cpu":    resource.MustParse("0"),
							"memory": resource.MustParse("100Gi"),
						},
						Allocation: map[string]resource.Quantity{
							"cpu":    resource.MustParse("0"),
							"memory": resource.MustParse("80Gi"),
						},
					},
				},
			},
			request: newBinpackRequest(8192, 4, 1, []string{"host1"}),
			opts: KVMBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceCPU: 1.0,
				},
			},
			expectedWeights: map[string]float64{
				"host1": 0, // CPU capacity is zero, skipped
			},
			wantErr: false,
		},
		{
			name: "hypervisor missing allocation for resource",
			hypervisors: []*hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capacity: map[string]resource.Quantity{
							"cpu": resource.MustParse("100"),
						},
						Allocation: map[string]resource.Quantity{
							// No CPU allocation
						},
					},
				},
			},
			request: newBinpackRequest(8192, 4, 1, []string{"host1"}),
			opts: KVMBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceCPU: 1.0,
				},
			},
			expectedWeights: map[string]float64{
				"host1": 0, // No allocation data, skipped
			},
			wantErr: false,
		},
		{
			name: "hypervisor missing capacity for resource",
			hypervisors: []*hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capacity: map[string]resource.Quantity{
							// No CPU capacity
						},
						Allocation: map[string]resource.Quantity{
							"cpu": resource.MustParse("80"),
						},
					},
				},
			},
			request: newBinpackRequest(8192, 4, 1, []string{"host1"}),
			opts: KVMBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceCPU: 1.0,
				},
			},
			expectedWeights: map[string]float64{
				"host1": 0, // No capacity data, skipped
			},
			wantErr: false,
		},
		{
			name: "high utilization scenario (over 100%)",
			hypervisors: []*hv1.Hypervisor{
				// Host with very little free resources
				newHypervisor("host1", "10", "10Gi", "1", "1Gi"),
			},
			request: newBinpackRequest(20480, 20, 1, []string{"host1"}), // 20Gi, 20 CPUs - more than available
			opts: KVMBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceCPU: 1.0,
				},
			},
			expectedWeights: map[string]float64{
				// (10 - 1 + 20) / 10 = 29/10 = 2.9 (over 100%)
				"host1": 2.9,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, 0, len(tt.hypervisors))
			for _, hv := range tt.hypervisors {
				objects = append(objects, hv)
			}

			step := &KVMBinpackStep{}
			step.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			step.Options = tt.opts

			result, err := step.Run(slog.Default(), tt.request)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			for host, expectedWeight := range tt.expectedWeights {
				actualWeight, ok := result.Activations[host]
				if !ok {
					t.Errorf("expected host %s to be in activations", host)
					continue
				}
				diff := actualWeight - expectedWeight
				if diff < 0 {
					diff = -diff
				}
				if diff > 0.1 { // tolerance of 0.1
					t.Errorf("for host %s, expected weight approximately %.2f, got %.2f", host, expectedWeight, actualWeight)
				}
			}

			// Verify statistics are populated
			if _, ok := result.Statistics["binpack score"]; !ok {
				t.Error("expected statistics to contain 'binpack score'")
			}
		})
	}
}

func TestKVMBinpackStep_calcVMResources(t *testing.T) {
	tests := []struct {
		name             string
		request          api.ExternalSchedulerRequest
		expectedMemBytes int64
		expectedCPU      int64
	}{
		{
			name:             "single instance with 8Gi memory and 4 CPUs",
			request:          newBinpackRequest(8192, 4, 1, []string{"host1"}),
			expectedMemBytes: 8192 * 1_000_000,
			expectedCPU:      4,
		},
		{
			name:             "multiple instances",
			request:          newBinpackRequest(4096, 2, 3, []string{"host1"}),
			expectedMemBytes: 4096 * 1_000_000 * 3,
			expectedCPU:      2 * 3,
		},
		{
			name:             "zero memory",
			request:          newBinpackRequest(0, 4, 1, []string{"host1"}),
			expectedMemBytes: 0,
			expectedCPU:      4,
		},
		{
			name:             "zero CPUs",
			request:          newBinpackRequest(8192, 0, 1, []string{"host1"}),
			expectedMemBytes: 8192 * 1_000_000,
			expectedCPU:      0,
		},
		{
			name:             "large values",
			request:          newBinpackRequest(524288, 128, 10, []string{"host1"}), // 512Gi, 128 CPUs, 10 instances
			expectedMemBytes: 524288 * 1_000_000 * 10,
			expectedCPU:      128 * 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &KVMBinpackStep{}
			resources := step.calcVMResources(tt.request)

			memResource, ok := resources[corev1.ResourceMemory]
			if !ok {
				t.Error("expected memory resource to be present")
			} else {
				actualMem := memResource.Value()
				if actualMem != tt.expectedMemBytes {
					t.Errorf("expected memory %d bytes, got %d", tt.expectedMemBytes, actualMem)
				}
			}

			cpuResource, ok := resources[corev1.ResourceCPU]
			if !ok {
				t.Error("expected CPU resource to be present")
			} else {
				actualCPU := cpuResource.Value()
				if actualCPU != tt.expectedCPU {
					t.Errorf("expected CPU %d, got %d", tt.expectedCPU, actualCPU)
				}
			}
		})
	}
}

func TestKVMBinpackStep_IndexRegistration(t *testing.T) {
	factory, ok := Index["kvm_binpack"]
	if !ok {
		t.Fatal("kvm_binpack not found in Index")
	}

	weigher := factory()
	if weigher == nil {
		t.Fatal("factory returned nil weigher")
	}

	_, ok = weigher.(*KVMBinpackStep)
	if !ok {
		t.Fatalf("expected *KVMBinpackStep, got %T", weigher)
	}
}
