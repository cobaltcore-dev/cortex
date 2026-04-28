// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// FlavorGroupCapacityConditionReady indicates the status data is up-to-date.
	FlavorGroupCapacityConditionReady = "Ready"
)

// FlavorGroupCapacitySpec defines the desired state of FlavorGroupCapacity.
type FlavorGroupCapacitySpec struct {
	// FlavorGroup is the name of the flavor group (e.g. "2101").
	// +kubebuilder:validation:Required
	FlavorGroup string `json:"flavorGroup"`

	// AvailabilityZone is the OpenStack AZ this capacity data covers (e.g. "qa-de-1a").
	// +kubebuilder:validation:Required
	AvailabilityZone string `json:"availabilityZone"`
}

// FlavorGroupCapacityStatus defines the observed state of FlavorGroupCapacity.
type FlavorGroupCapacityStatus struct {
	// TotalCapacity is the total schedulable slots in an empty-datacenter scenario.
	// Computed as sum of floor(EffectiveCapacity.Memory / smallestFlavorMemory) across
	// all hosts eligible for this flavor group (empty-state scheduler probe).
	// +kubebuilder:validation:Optional
	TotalCapacity int64 `json:"totalCapacity,omitempty"`

	// TotalHosts is the number of hosts eligible for this flavor group in the empty-state probe.
	// +kubebuilder:validation:Optional
	TotalHosts int64 `json:"totalHosts,omitempty"`

	// TotalPlaceable is the schedulable slots remaining given current VM allocations.
	// Computed from the current-state scheduler probe.
	// +kubebuilder:validation:Optional
	TotalPlaceable int64 `json:"totalPlaceable,omitempty"`

	// PlaceableHosts is the number of hosts still able to accept a new smallest-flavor VM.
	// +kubebuilder:validation:Optional
	PlaceableHosts int64 `json:"placeableHosts,omitempty"`

	// TotalInstances is the total number of VM instances running on hypervisors in this AZ,
	// derived from Hypervisor CRD Status.Instances (not filtered by flavor group).
	// +kubebuilder:validation:Optional
	TotalInstances int64 `json:"totalInstances,omitempty"`

	// CommittedCapacity is the sum of AcceptedAmount across Ready=True CommittedResource CRDs.
	// +kubebuilder:validation:Optional
	CommittedCapacity int64 `json:"committedCapacity,omitempty"`

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
// +kubebuilder:printcolumn:name="TotalCapacity",type="integer",JSONPath=".status.totalCapacity"
// +kubebuilder:printcolumn:name="TotalPlaceable",type="integer",JSONPath=".status.totalPlaceable"
// +kubebuilder:printcolumn:name="TotalHosts",type="integer",JSONPath=".status.totalHosts"
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
