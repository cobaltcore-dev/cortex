// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"github.com/go-logr/logr"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	sched "github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
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
				ResourceWeights: map[hv1.ResourceName]float64{
					hv1.ResourceMemory: 1.0,
					hv1.ResourceCPU:    1.0,
				},
			},
			wantError: false,
		},
		{
			name: "valid opts with only memory",
			opts: VMwareBinpackStepOpts{
				ResourceWeights: map[hv1.ResourceName]float64{
					hv1.ResourceMemory: 2.0,
				},
			},
			wantError: false,
		},
		{
			name: "valid opts with only cpu",
			opts: VMwareBinpackStepOpts{
				ResourceWeights: map[hv1.ResourceName]float64{
					hv1.ResourceCPU: 0.5,
				},
			},
			wantError: false,
		},
		{
			name: "invalid opts - empty resource weights",
			opts: VMwareBinpackStepOpts{
				ResourceWeights: map[hv1.ResourceName]float64{},
			},
			wantError: true,
		},
		{
			name: "invalid opts - unsupported resource",
			opts: VMwareBinpackStepOpts{
				ResourceWeights: map[hv1.ResourceName]float64{
					hv1.ResourceName("storage"): 1.0,
				},
			},
			wantError: true,
		},
		{
			name: "invalid opts - zero weight",
			opts: VMwareBinpackStepOpts{
				ResourceWeights: map[hv1.ResourceName]float64{
					hv1.ResourceMemory: 0.0,
				},
			},
			wantError: true,
		},
		{
			name: "invalid opts - negative weight",
			opts: VMwareBinpackStepOpts{
				ResourceWeights: map[hv1.ResourceName]float64{
					hv1.ResourceCPU: -1.0,
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
	step.Options.ResourceWeights = map[hv1.ResourceName]float64{
		hv1.ResourceMemory: 1.0,
		hv1.ResourceCPU:    1.0,
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
		request             sched.PlacementRequest
		expectedHigherScore string
		expectedLowerScore  string
	}{
		{
			name: "binpack prefers hosts with higher utilization",
			request: sched.PlacementRequest{
				NumInstances:     1,
				InstanceCapacity: api.CapacityFromVCPUsMemoryMB(1, 1000), // 1GB
				Hosts: []sched.Host{
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
			request: sched.PlacementRequest{
				NumInstances:     1,
				InstanceCapacity: api.CapacityFromVCPUsMemoryMB(1, 1000),
				Hosts: []sched.Host{
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
			result, err := step.Run(logr.Discard(), tt.request)
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

	// Memory capacity: 6000 MiB in bytes
	expectedMemoryBytes := int64(6000) * (1 << 20)
	memoryCapacity := capacity[hv1.ResourceMemory]
	if memoryCapacity.Value() != expectedMemoryBytes {
		t.Errorf("expected memory capacity %d, got %d",
			expectedMemoryBytes, memoryCapacity.Value())
	}

	// CPU capacity: 6
	expectedCPU := int64(6)
	cpuCapacity := capacity[hv1.ResourceCPU]
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

	// Memory allocation: 4000 MiB in bytes
	expectedMemoryBytes := int64(4000) * (1 << 20)
	memoryAllocation := allocation[hv1.ResourceMemory]
	if memoryAllocation.Value() != expectedMemoryBytes {
		t.Errorf("expected memory allocation %d, got %d",
			expectedMemoryBytes, memoryAllocation.Value())
	}

	// CPU allocation: 4
	expectedCPU := int64(4)
	cpuAllocation := allocation[hv1.ResourceCPU]
	if cpuAllocation.Value() != expectedCPU {
		t.Errorf("expected CPU allocation %d, got %d",
			expectedCPU, cpuAllocation.Value())
	}
}
