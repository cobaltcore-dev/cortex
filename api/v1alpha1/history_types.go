// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SchedulingIntent defines the intent of a scheduling decision.
type SchedulingIntent string

// Other intents can be defined by the operators.
const (
	// Used as default intent if the operator does not specify one.
	SchedulingIntentUnknown SchedulingIntent = "Unknown"
)

type SchedulingHistoryEntry struct {
	// The timestamp of when the decision was made.
	Timestamp metav1.Time `json:"timestamp"`
	// The pipeline that was used for the decision.
	PipelineRef corev1.ObjectReference `json:"pipelineRef"`
	// The intent of the decision (e.g., initial scheduling, rescheduling, etc.).
	Intent SchedulingIntent `json:"intent"`
	// All the possible hosts ordered by score for the decision.
	// The first entry is the host that should be selected.
	// Note: If the requester failed to schedule on the first host, the second host will be used.
	// +kubebuilder:validation:Optional
	OrderedHosts []string `json:"orderedHosts"`
}

type HistorySpec struct {
	// The scheduling domain this object with the history belongs to.
	SchedulingDomain SchedulingDomain `json:"schedulingDomain"`
	// The resource ID this history belongs to (e.g., the UUID of a nova instance).
	ResourceID string `json:"resourceID"`
}

type HistoryStatus struct {
	// The target host that was selected for the resource in the decision.
	// +kubebuilder:validation:Optional
	TargetHost string `json:"targetHost"`
	// The history of decisions for the resource.
	// +kubebuilder:validation:Optional
	History []SchedulingHistoryEntry `json:"history"`
	// A human-readable explanation of the scheduling history and decisions.
	// +kubebuilder:validation:Optional
	Explanation string `json:"explanation"`

	// Conditions represent the latest available observations of the history's state.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Domain",type="string",JSONPath=".spec.schedulingDomain"
// +kubebuilder:printcolumn:name="Resource ID",type="string",JSONPath=".spec.resourceID"
// +kubebuilder:printcolumn:name="Target Host",type="string",JSONPath=".status.targetHost"
// +kubebuilder:printcolumn:name="Created",type="date",JSONPath=".metadata.creationTimestamp"

// History is the Schema for the history API
type History struct {
	metav1.TypeMeta `json:",inline"`

	// Standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired state of History.
	// +required
	Spec HistorySpec `json:"spec"`
	// Status defines the observed state of History.
	// +optional
	Status HistoryStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HistoryList contains a list of History
type HistoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []History `json:"items"`
}

func init() {
	SchemeBuilder.Register(&History{}, &HistoryList{})
}
