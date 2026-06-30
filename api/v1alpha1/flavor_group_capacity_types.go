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

// FlavorCapacityStatus holds per-flavor scheduler probe results for one (flavor group × AZ) pair.
// These values come directly from scheduler probes and are independent of the cross-group
// capacity split (see FreeCapacity and ExclusivelyFreeCapacity on the parent status).
// "Placeable" means: if all remaining capacity in this AZ were used solely by this flavor,
// this is how many would fit. It does not account for competing flavor groups.
type FlavorCapacityStatus struct {
	// FlavorName is the OpenStack flavor name (e.g. "hana-v2-small").
	FlavorName string `json:"flavorName"`

	// PlaceableHosts is the number of hosts that can still fit this flavor given current allocations.
	// +kubebuilder:validation:Optional
	PlaceableHosts int64 `json:"placeableHosts,omitempty"`

	// PlaceableVMs is the number of VM slots remaining for this flavor given current allocations.
	// +kubebuilder:validation:Optional
	PlaceableVMs int64 `json:"placeableVms,omitempty"`

	// TotalCapacityHosts is the number of eligible hosts assuming an empty datacenter.
	// +kubebuilder:validation:Optional
	TotalCapacityHosts int64 `json:"totalCapacityHosts,omitempty"`

	// TotalCapacityVMSlots is the maximum number of VM slots assuming an empty datacenter.
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

	// CommittedCapacityBytes is CommittedCapacity converted to raw bytes.
	// +kubebuilder:validation:Optional
	CommittedCapacityBytes int64 `json:"committedCapacityBytes,omitempty"`

	// SmallestFlavorName is the name of the smallest flavor in this group, used as the
	// slot unit for ExclusivelyFreeSlots and related capacity fields.
	// +kubebuilder:validation:Optional
	SmallestFlavorName string `json:"smallestFlavorName,omitempty"`

	// TotalCapacity is the installed capacity across all eligible hosts in an empty-datacenter
	// scenario, expressed as raw resource amounts (bytes for memory, count for cores).
	// +kubebuilder:validation:Optional
	TotalCapacity map[string]resource.Quantity `json:"totalCapacity,omitempty"`

	// FreeCapacity is the sum of remaining resources across all candidate hosts for this
	// group given current allocations. Because groups can share hosts, the sum across groups
	// may exceed actual installed capacity — this field reflects per-group availability
	// before any cross-group fairness split.
	// +kubebuilder:validation:Optional
	FreeCapacity map[string]resource.Quantity `json:"freeCapacity,omitempty"`

	// ExclusivelyFreeCapacity is the share of remaining resources fairly attributed to this
	// group by the round-robin capacity split. The sum across all groups for an AZ never
	// exceeds actual installed capacity.
	// +kubebuilder:validation:Optional
	ExclusivelyFreeCapacity map[string]resource.Quantity `json:"exclusivelyFreeCapacity,omitempty"`

	// ExclusivelyFreeSlots is the number of smallest-flavor VM slots available from ExclusivelyFreeCapacity.
	// +kubebuilder:validation:Optional
	ExclusivelyFreeSlots int64 `json:"exclusivelyFreeSlots,omitempty"`

	// RunningInstances is the number of VMs running in this (flavor group × AZ) whose
	// flavor belongs to this group.
	// +kubebuilder:validation:Optional
	RunningInstances int64 `json:"runningInstances,omitempty"`

	// RunningResources is the total resource consumption of running VMs, keyed by resource type.
	// +kubebuilder:validation:Optional
	RunningResources map[string]resource.Quantity `json:"runningResources,omitempty"`

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
// +kubebuilder:printcolumn:name="Running",type="integer",JSONPath=".status.runningInstances"
// +kubebuilder:printcolumn:name="LastReconcile",type="date",JSONPath=".status.lastReconcileAt"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"

// FlavorGroupCapacity caches pre-computed capacity data for one flavor group in one AZ.
// One CRD exists per (flavor group × AZ) pair, updated by the capacity controller on a fixed interval.
// The capacity API reads these CRDs instead of probing the scheduler on each request.
type FlavorGroupCapacity struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`
	// +required
	Spec FlavorGroupCapacitySpec `json:"spec"`
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
