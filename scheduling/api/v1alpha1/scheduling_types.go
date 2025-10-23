// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SchedulingSpec struct {
	// The operator by which this scheduling should be extracted.
	Operator string `json:"operator,omitempty"`
}

type SchedulingStatus struct {
	// The time it took to schedule.
	// +kubebuilder:validation:Optional
	Took metav1.Duration `json:"took"`

	// If there was an error during the last scheduling, it is recorded here.
	// +kubebuilder:validation:Optional
	Error string `json:"error,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Operator",type="string",JSONPath=".spec.operator"
// +kubebuilder:printcolumn:name="Created",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Took",type="string",JSONPath=".status.took"
// +kubebuilder:printcolumn:name="Error",type="string",JSONPath=".status.error"

// Scheduling is the Schema for the schedulings API
type Scheduling struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Scheduling
	// +required
	Spec SchedulingSpec `json:"spec"`

	// status defines the observed state of Scheduling
	// +optional
	Status SchedulingStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// SchedulingList contains a list of Scheduling
type SchedulingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Scheduling `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Scheduling{}, &SchedulingList{})
}
