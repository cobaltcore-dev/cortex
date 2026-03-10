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

func newHypervisorWithCapacity(name, capacityCPU, capacityMem string) *hv1.Hypervisor {
	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: hv1.HypervisorStatus{
			Capacity: map[string]resource.Quantity{
				"cpu":    resource.MustParse(capacityCPU),
				"memory": resource.MustParse(capacityMem),
			},
		},
	}
}

func newPreferSmallerHostsRequest(hosts []string) api.ExternalSchedulerRequest {
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
		NumInstances: 1,
		Flavor: api.NovaObject[api.NovaFlavor]{
			Data: api.NovaFlavor{
				Name:       "m1.large",
				VCPUs:      4,
				MemoryMB:   8192,
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

func TestKVMPreferSmallerHostsStepOpts_Validate(t *testing.T) {
	tests := []struct {
		name    string
		opts    KVMPreferSmallerHostsStepOpts
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid opts with memory and cpu weights",
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 1.0,
					corev1.ResourceCPU:    1.0,
				},
			},
			wantErr: false,
		},
		{
			name: "valid opts with only memory weight",
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 2.0,
				},
			},
			wantErr: false,
		},
		{
			name: "valid opts with only cpu weight",
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceCPU: 0.5,
				},
			},
			wantErr: false,
		},
		{
			name: "valid opts with zero weights",
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 0.0,
					corev1.ResourceCPU:    0.0,
				},
			},
			wantErr: false,
		},
		{
			name: "invalid opts with empty resource weights",
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{},
			},
			wantErr: true,
			errMsg:  "at least one resource weight must be specified",
		},
		{
			name:    "invalid opts with nil resource weights",
			opts:    KVMPreferSmallerHostsStepOpts{},
			wantErr: true,
			errMsg:  "at least one resource weight must be specified",
		},
		{
			name: "invalid opts with negative weight",
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: -1.0,
				},
			},
			wantErr: true,
			errMsg:  "resource weights must be greater than or equal to zero",
		},
		{
			name: "invalid opts with negative cpu weight",
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceCPU: -0.5,
				},
			},
			wantErr: true,
			errMsg:  "resource weights must be greater than or equal to zero",
		},
		{
			name: "invalid opts with unsupported resource",
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceStorage: 1.0,
				},
			},
			wantErr: true,
			errMsg:  "unsupported resource",
		},
		{
			name: "invalid opts with unsupported ephemeral-storage resource",
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceEphemeralStorage: 1.0,
				},
			},
			wantErr: true,
			errMsg:  "unsupported resource",
		},
		{
			name: "invalid opts with custom unsupported resource",
			opts: KVMPreferSmallerHostsStepOpts{
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

func TestKVMPreferSmallerHostsStep_Run(t *testing.T) {
	scheme := buildTestScheme(t)

	tests := []struct {
		name            string
		hypervisors     []*hv1.Hypervisor
		request         api.ExternalSchedulerRequest
		opts            KVMPreferSmallerHostsStepOpts
		expectedWeights map[string]float64
		wantErr         bool
	}{
		{
			name: "smallest host gets highest score with memory weight only",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithCapacity("host1", "100", "64Gi"),  // smallest memory
				newHypervisorWithCapacity("host2", "100", "128Gi"), // middle memory
				newHypervisorWithCapacity("host3", "100", "256Gi"), // largest memory
			},
			request: newPreferSmallerHostsRequest([]string{"host1", "host2", "host3"}),
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 1.0,
				},
			},
			expectedWeights: map[string]float64{
				"host1": 1.0,   // smallest gets score 1
				"host2": 0.667, // (256-128)/(256-64) = 128/192 = 0.667
				"host3": 0.0,   // largest gets score 0
			},
			wantErr: false,
		},
		{
			name: "smallest host gets highest score with cpu weight only",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithCapacity("host1", "64", "100Gi"),  // smallest CPU
				newHypervisorWithCapacity("host2", "128", "100Gi"), // middle CPU
				newHypervisorWithCapacity("host3", "256", "100Gi"), // largest CPU
			},
			request: newPreferSmallerHostsRequest([]string{"host1", "host2", "host3"}),
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceCPU: 1.0,
				},
			},
			expectedWeights: map[string]float64{
				"host1": 1.0,   // smallest gets score 1
				"host2": 0.667, // (256-128)/(256-64) = 128/192 = 0.667
				"host3": 0.0,   // largest gets score 0
			},
			wantErr: false,
		},
		{
			name: "weighted average with both cpu and memory weights",
			hypervisors: []*hv1.Hypervisor{
				// host1: smallest memory (64Gi), largest CPU (256) -> mem score=1.0, cpu score=0.0
				newHypervisorWithCapacity("host1", "256", "64Gi"),
				// host2: middle memory (128Gi), middle CPU (128) -> mem score=0.667, cpu score=0.667
				newHypervisorWithCapacity("host2", "128", "128Gi"),
				// host3: largest memory (256Gi), smallest CPU (64) -> mem score=0.0, cpu score=1.0
				newHypervisorWithCapacity("host3", "64", "256Gi"),
			},
			request: newPreferSmallerHostsRequest([]string{"host1", "host2", "host3"}),
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 1.0,
					corev1.ResourceCPU:    1.0,
				},
			},
			expectedWeights: map[string]float64{
				// host1: (1.0 * 1.0 + 0.0 * 1.0) / 2.0 = 0.5
				"host1": 0.5,
				// host2: (0.667 * 1.0 + 0.667 * 1.0) / 2.0 = 0.667
				"host2": 0.667,
				// host3: (0.0 * 1.0 + 1.0 * 1.0) / 2.0 = 0.5
				"host3": 0.5,
			},
			wantErr: false,
		},
		{
			name: "different weights for cpu and memory",
			hypervisors: []*hv1.Hypervisor{
				// host1: smallest memory (64Gi), largest CPU (256) -> mem score=1.0, cpu score=0.0
				newHypervisorWithCapacity("host1", "256", "64Gi"),
				// host2: largest memory (256Gi), smallest CPU (64) -> mem score=0.0, cpu score=1.0
				newHypervisorWithCapacity("host2", "64", "256Gi"),
			},
			request: newPreferSmallerHostsRequest([]string{"host1", "host2"}),
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 2.0, // memory is weighted 2x
					corev1.ResourceCPU:    1.0,
				},
			},
			expectedWeights: map[string]float64{
				// host1: (1.0 * 2.0 + 0.0 * 1.0) / 3.0 = 2.0/3.0 = 0.667
				"host1": 0.667,
				// host2: (0.0 * 2.0 + 1.0 * 1.0) / 3.0 = 1.0/3.0 = 0.333
				"host2": 0.333,
			},
			wantErr: false,
		},
		{
			name: "two hosts with different sizes",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithCapacity("host1", "100", "100Gi"),
				newHypervisorWithCapacity("host2", "100", "200Gi"),
			},
			request: newPreferSmallerHostsRequest([]string{"host1", "host2"}),
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 1.0,
				},
			},
			expectedWeights: map[string]float64{
				"host1": 1.0, // smallest
				"host2": 0.0, // largest
			},
			wantErr: false,
		},
		{
			name: "all hosts have same memory capacity",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithCapacity("host1", "100", "128Gi"),
				newHypervisorWithCapacity("host2", "100", "128Gi"),
				newHypervisorWithCapacity("host3", "100", "128Gi"),
			},
			request: newPreferSmallerHostsRequest([]string{"host1", "host2", "host3"}),
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 1.0,
				},
			},
			expectedWeights: map[string]float64{
				// When all hosts have same capacity, resource is skipped, score is 0
				"host1": 0,
				"host2": 0,
				"host3": 0,
			},
			wantErr: false,
		},
		{
			name: "single host",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithCapacity("host1", "100", "128Gi"),
			},
			request: newPreferSmallerHostsRequest([]string{"host1"}),
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 1.0,
				},
			},
			expectedWeights: map[string]float64{
				// Single host means smallest == largest, resource is skipped
				"host1": 0,
			},
			wantErr: false,
		},
		{
			name:        "no hypervisors found",
			hypervisors: []*hv1.Hypervisor{},
			request:     newPreferSmallerHostsRequest([]string{"host1", "host2"}),
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 1.0,
				},
			},
			expectedWeights: map[string]float64{
				// No hypervisors means no capacity found, returns defaults
				"host1": 0,
				"host2": 0,
			},
			wantErr: false,
		},
		{
			name: "hypervisor missing for one host",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithCapacity("host1", "100", "64Gi"),
				newHypervisorWithCapacity("host2", "100", "128Gi"),
				// host3 hypervisor is missing
			},
			request: newPreferSmallerHostsRequest([]string{"host1", "host2", "host3"}),
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 1.0,
				},
			},
			expectedWeights: map[string]float64{
				"host1": 1.0, // smallest
				"host2": 0.0, // largest (among available)
				"host3": 0,   // missing hypervisor, skipped
			},
			wantErr: false,
		},
		{
			name: "hypervisor without memory capacity",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithCapacity("host1", "100", "64Gi"),
				newHypervisorWithCapacity("host2", "100", "128Gi"),
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Status: hv1.HypervisorStatus{
						Capacity: map[string]resource.Quantity{
							"cpu": resource.MustParse("100"),
							// No memory capacity
						},
					},
				},
			},
			request: newPreferSmallerHostsRequest([]string{"host1", "host2", "host3"}),
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 1.0,
				},
			},
			expectedWeights: map[string]float64{
				"host1": 1.0, // smallest
				"host2": 0.0, // largest (among those with memory)
				"host3": 0,   // no memory capacity, skipped
			},
			wantErr: false,
		},
		{
			name: "host filtered out by previous steps",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithCapacity("host1", "100", "64Gi"),
				newHypervisorWithCapacity("host2", "100", "128Gi"),
				newHypervisorWithCapacity("host3", "100", "256Gi"),
			},
			// Only host1 and host2 in the request (host3 was filtered out)
			request: newPreferSmallerHostsRequest([]string{"host1", "host2"}),
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 1.0,
				},
			},
			expectedWeights: map[string]float64{
				"host1": 1.0, // smallest among remaining
				"host2": 0.0, // largest among remaining
			},
			wantErr: false,
		},
		{
			name: "varied memory sizes - score calculation",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithCapacity("host1", "100", "100Gi"), // smallest
				newHypervisorWithCapacity("host2", "100", "150Gi"), // middle
				newHypervisorWithCapacity("host3", "100", "200Gi"), // largest
			},
			request: newPreferSmallerHostsRequest([]string{"host1", "host2", "host3"}),
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 1.0,
				},
			},
			expectedWeights: map[string]float64{
				// score = 1 - (mem - smallest) / (largest - smallest)
				"host1": 1.0, // 1 - (100-100)/(200-100) = 1 - 0/100 = 1.0
				"host2": 0.5, // 1 - (150-100)/(200-100) = 1 - 50/100 = 0.5
				"host3": 0.0, // 1 - (200-100)/(200-100) = 1 - 100/100 = 0.0
			},
			wantErr: false,
		},
		{
			name: "four hosts with varying sizes",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithCapacity("host1", "100", "64Gi"),
				newHypervisorWithCapacity("host2", "100", "96Gi"),
				newHypervisorWithCapacity("host3", "100", "128Gi"),
				newHypervisorWithCapacity("host4", "100", "192Gi"),
			},
			request: newPreferSmallerHostsRequest([]string{"host1", "host2", "host3", "host4"}),
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 1.0,
				},
			},
			expectedWeights: map[string]float64{
				// score = 1 - (mem - 64) / (192 - 64) = 1 - (mem - 64) / 128
				"host1": 1.0,  // 1 - 0/128 = 1.0
				"host2": 0.75, // 1 - 32/128 = 0.75
				"host3": 0.5,  // 1 - 64/128 = 0.5
				"host4": 0.0,  // 1 - 128/128 = 0.0
			},
			wantErr: false,
		},
		{
			name: "hypervisors with empty capacity map",
			hypervisors: []*hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capacity: map[string]resource.Quantity{},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status: hv1.HypervisorStatus{
						Capacity: map[string]resource.Quantity{},
					},
				},
			},
			request: newPreferSmallerHostsRequest([]string{"host1", "host2"}),
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 1.0,
				},
			},
			expectedWeights: map[string]float64{
				// No memory capacity found, returns defaults
				"host1": 0,
				"host2": 0,
			},
			wantErr: false,
		},
		{
			name: "very small difference in memory",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithCapacity("host1", "100", "128Gi"),
				newHypervisorWithCapacity("host2", "100", "129Gi"),
			},
			request: newPreferSmallerHostsRequest([]string{"host1", "host2"}),
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 1.0,
				},
			},
			expectedWeights: map[string]float64{
				"host1": 1.0, // smallest
				"host2": 0.0, // largest
			},
			wantErr: false,
		},
		{
			name: "extra hypervisors not in request",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithCapacity("host1", "100", "64Gi"),
				newHypervisorWithCapacity("host2", "100", "128Gi"),
				newHypervisorWithCapacity("host3", "100", "256Gi"), // not in request
				newHypervisorWithCapacity("host4", "100", "512Gi"), // not in request
			},
			request: newPreferSmallerHostsRequest([]string{"host1", "host2"}),
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 1.0,
				},
			},
			expectedWeights: map[string]float64{
				// Only hosts in request are considered for scoring
				"host1": 1.0, // smallest among requested
				"host2": 0.0, // largest among requested
			},
			wantErr: false,
		},
		{
			name: "mixed resource availability - only memory available",
			hypervisors: []*hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capacity: map[string]resource.Quantity{
							"memory": resource.MustParse("64Gi"),
							// No CPU
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status: hv1.HypervisorStatus{
						Capacity: map[string]resource.Quantity{
							"memory": resource.MustParse("128Gi"),
							// No CPU
						},
					},
				},
			},
			request: newPreferSmallerHostsRequest([]string{"host1", "host2"}),
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 1.0,
					corev1.ResourceCPU:    1.0, // CPU requested but not available
				},
			},
			expectedWeights: map[string]float64{
				// Only memory is considered since CPU is not available
				"host1": 1.0, // smallest memory
				"host2": 0.0, // largest memory
			},
			wantErr: false,
		},
		{
			name: "zero weight for memory, non-zero for cpu",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithCapacity("host1", "64", "256Gi"),  // smallest CPU, largest memory
				newHypervisorWithCapacity("host2", "128", "128Gi"), // middle
				newHypervisorWithCapacity("host3", "256", "64Gi"),  // largest CPU, smallest memory
			},
			request: newPreferSmallerHostsRequest([]string{"host1", "host2", "host3"}),
			opts: KVMPreferSmallerHostsStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 0.0, // zero weight - ignored
					corev1.ResourceCPU:    1.0,
				},
			},
			expectedWeights: map[string]float64{
				// Only CPU is considered since memory weight is 0
				"host1": 1.0,   // smallest CPU
				"host2": 0.667, // middle CPU
				"host3": 0.0,   // largest CPU
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

			step := &KVMPreferSmallerHostsStep{}
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
				if diff > 0.01 { // tolerance of 0.01
					t.Errorf("for host %s, expected weight approximately %.3f, got %.3f", host, expectedWeight, actualWeight)
				}
			}

			// Verify statistics are populated
			if _, ok := result.Statistics["small host score"]; !ok {
				t.Error("expected statistics to contain 'small host score'")
			}
		})
	}
}

func TestKVMPreferSmallerHostsStep_IndexRegistration(t *testing.T) {
	factory, ok := Index["kvm_prefer_smaller_hosts"]
	if !ok {
		t.Fatal("kvm_prefer_smaller_hosts not found in Index")
	}

	weigher := factory()
	if weigher == nil {
		t.Fatal("factory returned nil weigher")
	}

	_, ok = weigher.(*KVMPreferSmallerHostsStep)
	if !ok {
		t.Fatalf("expected *KVMPreferSmallerHostsStep, got %T", weigher)
	}
}
