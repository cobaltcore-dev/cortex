// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

// Package testhelpers provides shared test utilities for Nova scheduling tests.
package testhelpers

import (
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// BuildTestScheme creates a runtime.Scheme with v1alpha1 and hv1 types registered.
func BuildTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add v1alpha1 scheme: %v", err)
	}
	if err := hv1.SchemeBuilder.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add hv1 scheme: %v", err)
	}
	return scheme
}

// ============================================================================
// Hypervisor Builder
// ============================================================================

// HypervisorArgs contains arguments for building a Hypervisor.
type HypervisorArgs struct {
	Name      string
	CPUCap    string // default: "16"
	CPUAlloc  string // default: "0"
	MemCap    string // default: "32Gi"
	MemAlloc  string // default: "0"
	Namespace string // default: ""
}

// NewHypervisor creates a Hypervisor with the given arguments.
// Only Name is required; other fields have sensible defaults.
func NewHypervisor(h HypervisorArgs) *hv1.Hypervisor {
	// Apply defaults
	if h.CPUCap == "" {
		h.CPUCap = "16"
	}
	if h.CPUAlloc == "" {
		h.CPUAlloc = "0"
	}
	if h.MemCap == "" {
		h.MemCap = "32Gi"
	}
	if h.MemAlloc == "" {
		h.MemAlloc = "0"
	}

	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      h.Name,
			Namespace: h.Namespace,
		},
		Status: hv1.HypervisorStatus{
			Capacity: map[string]resource.Quantity{
				"cpu":    resource.MustParse(h.CPUCap),
				"memory": resource.MustParse(h.MemCap),
			},
			Allocation: map[string]resource.Quantity{
				"cpu":    resource.MustParse(h.CPUAlloc),
				"memory": resource.MustParse(h.MemAlloc),
			},
		},
	}
}

// ============================================================================
// Reservation Builder
// ============================================================================

// ReservationArgs contains arguments for building a Reservation.
type ReservationArgs struct {
	Name         string
	TargetHost   string                       // Spec.TargetHost
	ObservedHost string                       // Status.Host (defaults to TargetHost if not set)
	Type         v1alpha1.ReservationType     // default: CommittedResource
	CPU          string                       // default: "4"
	Memory       string                       // default: "8Gi"
	Failed       bool                         // if true, sets Ready condition to False
	Namespace    string                       // default: ""
	Resources    map[string]resource.Quantity // overrides CPU/Memory if set

	// CommittedResource-specific fields
	ProjectID    string // default: "project-A"
	ResourceName string // default: "m1.large" (flavor name)

	// Failover-specific fields
	ResourceGroup string            // default: "m1.large"
	Allocations   map[string]string // VM UUID -> original host mapping
}

// Ptr returns a pointer to the given value.
func Ptr[T any](v T) *T { return &v }

// NewReservation creates a Reservation with the given arguments.
// Only Name is required; other fields have sensible defaults.
// Type defaults to CommittedResource if not set.
func NewReservation(r ReservationArgs) *v1alpha1.Reservation {
	if r.Type == "" {
		r.Type = v1alpha1.ReservationTypeCommittedResource
	}
	if r.CPU == "" {
		r.CPU = "4"
	}
	if r.Memory == "" {
		r.Memory = "8Gi"
	}
	if r.ProjectID == "" {
		r.ProjectID = "project-A"
	}
	if r.ResourceName == "" {
		r.ResourceName = "m1.large"
	}
	if r.ResourceGroup == "" {
		r.ResourceGroup = "m1.large"
	}
	// Default ObservedHost to TargetHost if not explicitly set
	observedHost := r.ObservedHost
	if observedHost == "" && r.TargetHost != "" {
		observedHost = r.TargetHost
	}

	// Build resources
	resources := r.Resources
	if resources == nil {
		resources = map[string]resource.Quantity{
			"cpu":    resource.MustParse(r.CPU),
			"memory": resource.MustParse(r.Memory),
		}
	}

	conditions := []metav1.Condition{
		{
			Type:   v1alpha1.ReservationConditionReady,
			Status: metav1.ConditionTrue,
			Reason: "ReservationActive",
		},
	}
	if r.Failed {
		conditions[0].Status = metav1.ConditionFalse
		conditions[0].Reason = "ReservationFailed"
	}

	res := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.Name,
			Namespace: r.Namespace,
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       r.Type,
			TargetHost: r.TargetHost,
			Resources:  resources,
		},
		Status: v1alpha1.ReservationStatus{
			Conditions: conditions,
			Host:       observedHost,
		},
	}

	// Set type-specific fields
	switch r.Type {
	case v1alpha1.ReservationTypeCommittedResource:
		res.Spec.CommittedResourceReservation = &v1alpha1.CommittedResourceReservationSpec{
			ProjectID:    r.ProjectID,
			ResourceName: r.ResourceName,
		}
	case v1alpha1.ReservationTypeFailover:
		res.Spec.FailoverReservation = &v1alpha1.FailoverReservationSpec{
			ResourceGroup: r.ResourceGroup,
		}
		if r.Allocations != nil {
			res.Status.FailoverReservation = &v1alpha1.FailoverReservationStatus{
				Allocations: r.Allocations,
			}
		}
	}

	return res
}

// NewFailoverReservation is a convenience function for creating failover reservations.
func NewFailoverReservation(r ReservationArgs) *v1alpha1.Reservation {
	r.Type = v1alpha1.ReservationTypeFailover
	return NewReservation(r)
}

// NewCommittedReservation is a convenience function for creating committed resource reservations.
func NewCommittedReservation(r ReservationArgs) *v1alpha1.Reservation {
	r.Type = v1alpha1.ReservationTypeCommittedResource
	return NewReservation(r)
}

// NewReservations creates multiple Reservations from a slice of ReservationArgs.
func NewReservations(args []ReservationArgs) []*v1alpha1.Reservation {
	reservations := make([]*v1alpha1.Reservation, len(args))
	for i, r := range args {
		reservations[i] = NewReservation(r)
	}
	return reservations
}

// ============================================================================
// Nova Request Builder
// ============================================================================

// NovaRequestArgs contains arguments for building a Nova ExternalSchedulerRequest.
type NovaRequestArgs struct {
	InstanceUUID string             // default: "instance-123"
	ProjectID    string             // default: "project-A"
	FlavorName   string             // default: "m1.large"
	VCPUs        int                // default: 4
	Memory       string             // default: "8Gi" (parsed to MB for Nova API)
	NumInstances int                // default: 1
	Evacuation   bool               // default: false
	Hosts        []string           // required
	Pipeline     string             // optional pipeline name
	Weights      map[string]float64 // optional weights per host (defaults to 1.0 for all hosts)

	// Flavor extra specs
	HypervisorType string // default: "qemu"
	ExtraSpecs     map[string]string

	// Additional scheduler hints
	SchedulerHints map[string]any
}

// parseMemoryToMB converts a memory string (e.g., "8Gi", "4096Mi") to megabytes.
func parseMemoryToMB(memory string) uint64 {
	q := resource.MustParse(memory)
	// Convert to bytes, then to MB
	bytes := q.Value()
	return uint64(bytes / (1024 * 1024)) //nolint:gosec // test code
}

// NewNovaRequest creates a Nova ExternalSchedulerRequest with the given arguments.
// Only Hosts is required; other fields have sensible defaults.
func NewNovaRequest(r NovaRequestArgs) api.ExternalSchedulerRequest {
	// Apply defaults
	if r.InstanceUUID == "" {
		r.InstanceUUID = "instance-123"
	}
	if r.ProjectID == "" {
		r.ProjectID = "project-A"
	}
	if r.FlavorName == "" {
		r.FlavorName = "m1.large"
	}
	if r.VCPUs == 0 {
		r.VCPUs = 4
	}
	if r.Memory == "" {
		r.Memory = "8Gi"
	}
	if r.NumInstances == 0 {
		r.NumInstances = 1
	}

	// Build host list
	hostList := make([]api.ExternalSchedulerHost, len(r.Hosts))
	for i, h := range r.Hosts {
		hostList[i] = api.ExternalSchedulerHost{ComputeHost: h}
	}

	// Apply default for HypervisorType
	if r.HypervisorType == "" {
		r.HypervisorType = "qemu"
	}

	// Build extra specs
	extraSpecs := r.ExtraSpecs
	if extraSpecs == nil {
		extraSpecs = make(map[string]string)
	}
	extraSpecs["capabilities:hypervisor_type"] = r.HypervisorType

	// Build scheduler hints
	schedulerHints := r.SchedulerHints
	if r.Evacuation {
		if schedulerHints == nil {
			schedulerHints = make(map[string]any)
		}
		schedulerHints["_nova_check_type"] = []any{"evacuate"}
	}

	// Convert memory string to MB
	memoryMB := parseMemoryToMB(r.Memory)

	spec := api.NovaSpec{
		ProjectID:      r.ProjectID,
		InstanceUUID:   r.InstanceUUID,
		NumInstances:   uint64(r.NumInstances), //nolint:gosec // test code
		SchedulerHints: schedulerHints,
		Flavor: api.NovaObject[api.NovaFlavor]{
			Data: api.NovaFlavor{
				Name:       r.FlavorName,
				VCPUs:      uint64(r.VCPUs), //nolint:gosec // test code
				MemoryMB:   memoryMB,
				ExtraSpecs: extraSpecs,
			},
		},
	}

	// Build weights - default to 1.0 for all hosts if not specified
	weights := r.Weights
	if weights == nil {
		weights = make(map[string]float64)
		for _, h := range r.Hosts {
			weights[h] = 1.0
		}
	}

	req := api.ExternalSchedulerRequest{
		Spec:    api.NovaObject[api.NovaSpec]{Data: spec},
		Hosts:   hostList,
		Weights: weights,
	}

	if r.Pipeline != "" {
		req.Pipeline = r.Pipeline
	}

	return req
}

// ============================================================================
// Convenience Functions for Simple Cases
// ============================================================================

// SimpleHypervisor creates a hypervisor with just name and free capacity.
// Free capacity is calculated as capacity - allocation.
func SimpleHypervisor(name, cpuFree, memFree string) *hv1.Hypervisor {
	// Parse the free values to calculate capacity (assuming 0 allocation)
	return NewHypervisor(HypervisorArgs{
		Name:     name,
		CPUCap:   cpuFree,
		CPUAlloc: "0",
		MemCap:   memFree,
		MemAlloc: "0",
	})
}

// SimpleFailoverReservation creates a failover reservation with minimal arguments.
func SimpleFailoverReservation(name, host string, allocations map[string]string) *v1alpha1.Reservation {
	return NewFailoverReservation(ReservationArgs{
		Name:        name,
		TargetHost:  host,
		Allocations: allocations,
	})
}

// SimpleCommittedReservation creates a committed resource reservation with minimal arguments.
func SimpleCommittedReservation(name, host, projectID, flavorName string) *v1alpha1.Reservation {
	return NewCommittedReservation(ReservationArgs{
		Name:         name,
		TargetHost:   host,
		ProjectID:    projectID,
		ResourceName: flavorName,
	})
}

// SimpleNovaRequest creates a Nova request with minimal arguments.
func SimpleNovaRequest(instanceUUID string, evacuation bool, hosts ...string) api.ExternalSchedulerRequest {
	return NewNovaRequest(NovaRequestArgs{
		InstanceUUID: instanceUUID,
		Evacuation:   evacuation,
		Hosts:        hosts,
	})
}
