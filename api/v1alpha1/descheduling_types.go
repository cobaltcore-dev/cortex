// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The type of reference to the virtual machine that should be descheduled.
type DeschedulingSpecVMReferenceType string

const (
	// Openstack server uuid.
	DeschedulingSpecVMReferenceNovaServerUUID DeschedulingSpecVMReferenceType = "novaServerUUID"
)

// The type of host from which the virtual machine should be descheduled.
type DeschedulingSpecHostType string

const (
	// The host is identified by its compute host name.
	DeschedulingSpecHostTypeNovaComputeHostName DeschedulingSpecHostType = "novaComputeHostName"
)

type DeschedulingSpec struct {
	// A reference to the virtual machine that should be descheduled.
	Ref string `json:"ref,omitempty"`
	// The type of reference used to identify the virtual machine.
	RefType DeschedulingSpecVMReferenceType `json:"refType,omitempty"`
	// The name of the compute host from which the virtual machine should be descheduled.
	PrevHost string `json:"prevHost,omitempty"`
	// The type of host from which the virtual machine should be descheduled.
	PrevHostType DeschedulingSpecHostType `json:"prevHostType,omitempty"`
	// The human-readable reason why the VM should be descheduled.
	Reason string `json:"reason,omitempty"`
}

const (
	// The descheduling was successfully processed.
	DeschedulingConditionReady = "Ready"
	// The descheduling is currently being processed.
	DeschedulingConditionInProgress = "InProgress"
)

type DeschedulingStatus struct {
	// The current status conditions of the descheduling.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
	// The name of the compute host where the VM was rescheduled to.
	NewHost string `json:"newHost,omitempty"`
	// The type of host where the VM was rescheduled to.
	NewHostType DeschedulingSpecHostType `json:"newHostType,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Previous Host",type="string",JSONPath=".spec.prevHost"
// +kubebuilder:printcolumn:name="New Host",type="string",JSONPath=".status.newHost"
// +kubebuilder:printcolumn:name="Created",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Reason(s)",type="string",JSONPath=".spec.reason"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"

// Descheduling is the Schema for the deschedulings API
type Descheduling struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Descheduling
	// +required
	Spec DeschedulingSpec `json:"spec"`

	// status defines the observed state of Descheduling
	// +optional
	Status DeschedulingStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// DeschedulingList contains a list of Descheduling
type DeschedulingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Descheduling `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Descheduling{}, &DeschedulingList{})
}
