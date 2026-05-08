// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// FlavorGroupCapacityConditionReady indicates the status data is up-to-date.
	FlavorGroupCapacityConditionReady = "Ready"
)

// FlavorGroupCapacitySpec defines the desired state of FlavorGroupCapacity.
type FlavorGroupCapacitySpec struct {
	// FlavorGroup is the name of the flavor group (e.g. "hana-v2").
	// +kubebuilder:validation:Required
	FlavorGroup string `json:"flavorGroup"`

	// AvailabilityZone is the OpenStack AZ this capacity data covers (e.g. "qa-de-1a").
	// +kubebuilder:validation:Required
	AvailabilityZone string `json:"availabilityZone"`
}

// FlavorCapacityStatus holds per-flavor capacity numbers for one (flavor group × AZ) pair.
type FlavorCapacityStatus struct {
	// FlavorName is the OpenStack flavor name (e.g. "hana-v2-small").
	FlavorName string `json:"flavorName"`

	// PlaceableHosts is the number of hosts that can still fit this flavor given current allocations.
	// +kubebuilder:validation:Optional
	PlaceableHosts int64 `json:"placeableHosts,omitempty"`

	// PlaceableVMs is the number of VM slots remaining for this flavor given current allocations.
	// +kubebuilder:validation:Optional
	PlaceableVMs int64 `json:"placeableVms,omitempty"`

	// TotalCapacityHosts is the number of eligible hosts in an empty-datacenter scenario.
	// +kubebuilder:validation:Optional
	TotalCapacityHosts int64 `json:"totalCapacityHosts,omitempty"`

	// TotalCapacityVMSlots is the maximum number of VM slots in an empty-datacenter scenario.
	// +kubebuilder:validation:Optional
	TotalCapacityVMSlots int64 `json:"totalCapacityVmSlots,omitempty"`
}

// FlavorGroupCapacityStatus defines the observed state of FlavorGroupCapacity.
type FlavorGroupCapacityStatus struct {
	// Flavors holds per-flavor capacity data for all flavors in the group.
	// +kubebuilder:validation:Optional
	Flavors []FlavorCapacityStatus `json:"flavors,omitempty"`

	// CommittedCapacity is the sum of AcceptedAmount across active CommittedResource CRDs,
	// expressed in multiples of the smallest flavor's memory.
	// +kubebuilder:validation:Optional
	CommittedCapacity int64 `json:"committedCapacity,omitempty"`

	// TotalCapacity is the total capacity of all eligible hosts in an empty-datacenter scenario.
	// +kubebuilder:validation:Optional
	TotalCapacity map[string]resource.Quantity `json:"totalCapacity,omitempty"`

	// TotalInstances is the total number of VM instances running on hypervisors in this AZ,
	// derived from Hypervisor CRD Status.Instances (not filtered by flavor group).
	// +kubebuilder:validation:Optional
	TotalInstances int64 `json:"totalInstances,omitempty"`

	// LastReconcileAt is the timestamp of the last successful reconcile.
	// +kubebuilder:validation:Optional
	LastReconcileAt metav1.Time `json:"lastReconcileAt,omitempty"`

	// The current status conditions of the FlavorGroupCapacity.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="FlavorGroup",type="string",JSONPath=".spec.flavorGroup"
// +kubebuilder:printcolumn:name="AZ",type="string",JSONPath=".spec.availabilityZone"
// +kubebuilder:printcolumn:name="TotalInstances",type="integer",JSONPath=".status.totalInstances"
// +kubebuilder:printcolumn:name="LastReconcile",type="date",JSONPath=".status.lastReconcileAt"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"

// FlavorGroupCapacity caches pre-computed capacity data for one flavor group in one AZ.
// One CRD exists per (flavor group × AZ) pair, updated by the capacity controller on a fixed interval.
// The capacity API reads these CRDs instead of probing the scheduler on each request.
type FlavorGroupCapacity struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of FlavorGroupCapacity
	// +required
	Spec FlavorGroupCapacitySpec `json:"spec"`

	// status defines the observed state of FlavorGroupCapacity
	// +optional
	Status FlavorGroupCapacityStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// FlavorGroupCapacityList contains a list of FlavorGroupCapacity.
type FlavorGroupCapacityList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FlavorGroupCapacity `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FlavorGroupCapacity{}, &FlavorGroupCapacityList{})
}
