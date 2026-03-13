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
	// The top hosts ordered by score for the decision (limited to 3).
	// This is not a complete list of all candidates — only the highest-ranked
	// hosts are retained to keep the history compact.
	// +kubebuilder:validation:Optional,MaxItems=3
	OrderedHosts []string `json:"orderedHosts,omitempty"`
	// Whether the scheduling decision was successful.
	// +kubebuilder:validation:Optional
	Successful bool `json:"successful"`
}

type HistorySpec struct {
	// The scheduling domain this object with the history belongs to.
	SchedulingDomain SchedulingDomain `json:"schedulingDomain"`
	// The resource ID this history belongs to (e.g., the UUID of a nova instance).
	ResourceID string `json:"resourceID"`
	// The availability zone of the resource, if known. Only set for scheduling
	// domains that provide AZ information (e.g., Nova).
	// +kubebuilder:validation:Optional
	AvailabilityZone *string `json:"availabilityZone,omitempty"`
}

// CurrentDecision holds the full context of the most recent scheduling
// decision. When a new decision arrives the previous CurrentDecision is
// compacted into a SchedulingHistoryEntry and appended to History.
type CurrentDecision struct {
	// The timestamp of when the decision was made.
	Timestamp metav1.Time `json:"timestamp"`
	// The pipeline that was used for the decision.
	PipelineRef corev1.ObjectReference `json:"pipelineRef"`
	// The intent of the decision (e.g., initial scheduling, rescheduling, etc.).
	Intent SchedulingIntent `json:"intent"`
	// Whether the scheduling decision was successful.
	Successful bool `json:"successful"`
	// The target host selected for the resource. nil when no host was found.
	// +kubebuilder:validation:Optional
	TargetHost *string `json:"targetHost,omitempty"`
	// A human-readable explanation of the scheduling decision.
	// +kubebuilder:validation:Optional
	Explanation string `json:"explanation,omitempty"`
	// The top hosts ordered by score (limited to 3).
	// +kubebuilder:validation:Optional
	OrderedHosts []string `json:"orderedHosts,omitempty"`
}

type HistoryStatus struct {
	// Current represents the latest scheduling decision with full context.
	// +kubebuilder:validation:Optional
	Current CurrentDecision `json:"current,omitempty"`
	// History of past scheduling decisions (limited to last 10).
	// +kubebuilder:validation:Optional
	History []SchedulingHistoryEntry `json:"history,omitempty"`

	// Conditions represent the latest available observations of the history's state.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Domain",type="string",JSONPath=".spec.schedulingDomain"
// +kubebuilder:printcolumn:name="Resource ID",type="string",JSONPath=".spec.resourceID"
// +kubebuilder:printcolumn:name="AZ",type="string",JSONPath=".spec.availabilityZone"
// +kubebuilder:printcolumn:name="Target Host",type="string",JSONPath=".status.current.targetHost"
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].reason"
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
