// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestVMwareBinpackStepOpts_Validate(t *testing.T) {
	tests := []struct {
		name      string
		opts      VMwareBinpackStepOpts
		wantError bool
	}{
		{
			name: "valid opts with memory and cpu",
			opts: VMwareBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 1.0,
					corev1.ResourceCPU:    1.0,
				},
			},
			wantError: false,
		},
		{
			name: "valid opts with only memory",
			opts: VMwareBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 2.0,
				},
			},
			wantError: false,
		},
		{
			name: "valid opts with only cpu",
			opts: VMwareBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceCPU: 0.5,
				},
			},
			wantError: false,
		},
		{
			name: "invalid opts - empty resource weights",
			opts: VMwareBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{},
			},
			wantError: true,
		},
		{
			name: "invalid opts - unsupported resource",
			opts: VMwareBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceStorage: 1.0,
				},
			},
			wantError: true,
		},
		{
			name: "invalid opts - zero weight",
			opts: VMwareBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceMemory: 0.0,
				},
			},
			wantError: true,
		},
		{
			name: "invalid opts - negative weight",
			opts: VMwareBinpackStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceCPU: -1.0,
				},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()
			if (err != nil) != tt.wantError {
				t.Errorf("Validate() error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

func TestVMwareBinpackStep_Run(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// The binpack algorithm calculates: (used_space + vm_request) / capacity
	// where used_space = allocation (currently used resources)
	// So hosts with MORE used space get HIGHER scores (tighter packing)
	// This means hosts with HIGHER utilization get HIGHER binpack scores
	hostUtilizations, err := v1alpha1.BoxFeatureList([]any{
		// host1: 50% memory utilization, 50% CPU utilization
		// capacity = 10000, allocation = 5000
		// score = (5000 + 1000) / 10000 = 0.6
		&compute.HostUtilization{
			ComputeHost:           "host1",
			RAMUsedMB:             5000,
			TotalRAMAllocatableMB: 5000, // capacity = 5000 + 5000 = 10000
			VCPUsUsed:             5,
			TotalVCPUsAllocatable: 5, // capacity = 5 + 5 = 10
		},
		// host2: low utilization - 10% memory, 10% CPU
		// capacity = 10000, allocation = 1000
		// score = (1000 + 1000) / 10000 = 0.2
		&compute.HostUtilization{
			ComputeHost:           "host2",
			RAMUsedMB:             1000,
			TotalRAMAllocatableMB: 9000, // capacity = 9000 + 1000 = 10000
			VCPUsUsed:             1,
			TotalVCPUsAllocatable: 9, // capacity = 9 + 1 = 10
		},
		// host3: high utilization - 90% memory, 90% CPU
		// capacity = 10000, allocation = 9000
		// score = (9000 + 1000) / 10000 = 1.0
		&compute.HostUtilization{
			ComputeHost:           "host3",
			RAMUsedMB:             9000,
			TotalRAMAllocatableMB: 1000, // capacity = 1000 + 9000 = 10000
			VCPUsUsed:             9,
			TotalVCPUsAllocatable: 1, // capacity = 1 + 9 = 10
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	step := &VMwareBinpackStep{}
	step.Options.ResourceWeights = map[corev1.ResourceName]float64{
		corev1.ResourceMemory: 1.0,
		corev1.ResourceCPU:    1.0,
	}
	step.Client = fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&v1alpha1.Knowledge{
			ObjectMeta: metav1.ObjectMeta{Name: "host-utilization"},
			Status:     v1alpha1.KnowledgeStatus{Raw: hostUtilizations},
		}).
		Build()

	tests := []struct {
		name                string
		request             api.ExternalSchedulerRequest
		expectedHigherScore string
		expectedLowerScore  string
	}{
		{
			name: "binpack prefers hosts with higher utilization",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								MemoryMB: 1000, // 1GB
								VCPUs:    1,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			// host3 has higher utilization -> higher binpack score (1.0)
			// host2 has lower utilization -> lower binpack score (0.2)
			expectedHigherScore: "host3",
			expectedLowerScore:  "host2",
		},
		{
			name: "handles missing host data gracefully",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								MemoryMB: 1000,
								VCPUs:    1,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "unknown_host"}, // No utilization data
				},
			},
			expectedHigherScore: "host1",
			expectedLowerScore:  "unknown_host", // Should have score 0 (default)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := step.Run(slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			higherScore := result.Activations[tt.expectedHigherScore]
			lowerScore := result.Activations[tt.expectedLowerScore]

			if higherScore <= lowerScore {
				t.Errorf("expected %s (score: %f) to have higher score than %s (score: %f)",
					tt.expectedHigherScore, higherScore, tt.expectedLowerScore, lowerScore)
			}
		})
	}
}

func TestVMwareBinpackStep_CalcHostCapacity(t *testing.T) {
	step := &VMwareBinpackStep{}

	hostUtilization := compute.HostUtilization{
		ComputeHost:           "test-host",
		RAMUsedMB:             4000,
		TotalRAMAllocatableMB: 6000, // Capacity = 6000 + 4000 = 10000 MB
		VCPUsUsed:             4,
		TotalVCPUsAllocatable: 6, // Capacity = 6 + 4 = 10 vCPUs
	}

	capacity := step.calcHostCapacity(hostUtilization)

	// Memory capacity: 6000 * 1_000_000 = 6_000_000_000 bytes
	expectedMemoryBytes := int64(6000) * 1_000_000
	memoryCapacity := capacity[corev1.ResourceMemory]
	if memoryCapacity.Value() != expectedMemoryBytes {
		t.Errorf("expected memory capacity %d, got %d",
			expectedMemoryBytes, memoryCapacity.Value())
	}

	// CPU capacity: 6
	expectedCPU := int64(6)
	cpuCapacity := capacity[corev1.ResourceCPU]
	if cpuCapacity.Value() != expectedCPU {
		t.Errorf("expected CPU capacity %d, got %d",
			expectedCPU, cpuCapacity.Value())
	}
}

func TestVMwareBinpackStep_CalcHostAllocation(t *testing.T) {
	step := &VMwareBinpackStep{}

	hostUtilization := compute.HostUtilization{
		ComputeHost:           "test-host",
		RAMUsedMB:             4000,
		TotalRAMAllocatableMB: 6000,
		VCPUsUsed:             4,
		TotalVCPUsAllocatable: 6,
	}

	allocation := step.calcHostAllocation(hostUtilization)

	// Memory allocation: 4000 * 1_000_000 = 4_000_000_000 bytes
	expectedMemoryBytes := int64(4000) * 1_000_000
	memoryAllocation := allocation[corev1.ResourceMemory]
	if memoryAllocation.Value() != expectedMemoryBytes {
		t.Errorf("expected memory allocation %d, got %d",
			expectedMemoryBytes, memoryAllocation.Value())
	}

	// CPU allocation: 4
	expectedCPU := int64(4)
	cpuAllocation := allocation[corev1.ResourceCPU]
	if cpuAllocation.Value() != expectedCPU {
		t.Errorf("expected CPU allocation %d, got %d",
			expectedCPU, cpuAllocation.Value())
	}
}

func TestVMwareBinpackStep_CalcVMResources(t *testing.T) {
	step := &VMwareBinpackStep{}

	tests := []struct {
		name           string
		request        api.ExternalSchedulerRequest
		expectedMemory int64
		expectedCPU    int64
	}{
		{
			name: "single instance",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								MemoryMB: 2048,
								VCPUs:    4,
							},
						},
					},
				},
			},
			expectedMemory: 2048 * 1_000_000, // 2048 MB in bytes
			expectedCPU:    4,
		},
		{
			name: "multiple instances",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 3,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								MemoryMB: 1024,
								VCPUs:    2,
							},
						},
					},
				},
			},
			expectedMemory: 3 * 1024 * 1_000_000, // 3 instances * 1024 MB in bytes
			expectedCPU:    3 * 2,                // 3 instances * 2 vCPUs
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resources := step.calcVMResources(tt.request)

			memoryResources := resources[corev1.ResourceMemory]
			if memoryResources.Value() != tt.expectedMemory {
				t.Errorf("expected memory %d, got %d",
					tt.expectedMemory, memoryResources.Value())
			}

			cpuResources := resources[corev1.ResourceCPU]
			if cpuResources.Value() != tt.expectedCPU {
				t.Errorf("expected CPU %d, got %d",
					tt.expectedCPU, cpuResources.Value())
			}
		})
	}
}
