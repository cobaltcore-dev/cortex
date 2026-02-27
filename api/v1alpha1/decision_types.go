// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SchedulingIntents represents the Intent for a scheduling event.
type SchedulingIntent string

const (
	// SchedulingIntentInitialPlacement indicates that this is the initial placement of a resource.
	SchedulingIntentInitialPlacement SchedulingIntent = "InitialPlacement"
	// SchedulingIntentLiveMigration  indicates that this scheduling event is triggered by a live migration operation.
	SchedulingIntentLiveMigration SchedulingIntent = "LiveMigration"
	// SchedulingIntentResize indicates that this scheduling event is triggered by a resize operation.
	SchedulingIntentResize SchedulingIntent = "Resize"
	// SchedulingIntentRebuild indicates that this scheduling event is triggered by a rebuild operation.
	SchedulingIntentRebuild SchedulingIntent = "Rebuild"
	// SchedulingIntentEvacuate indicates that this scheduling event is triggered by an evacuate operation.
	SchedulingIntentEvacuate SchedulingIntent = "Evacuate"
	// SchedulingIntentUnknown indicates that the Intent for this scheduling event is unknown.
	SchedulingIntentUnknown SchedulingIntent = "Unknown"
)

// SchedulingHistoryEntry represents a single entry in the scheduling history of a resource.
type SchedulingHistoryEntry struct {
	// The hosts that were selected in this scheduling event, in order of preference.
	OrderedHosts []string `json:"orderedHosts"`
	// Timestamp of when the scheduling event occurred.
	Timestamp metav1.Time `json:"timestamp"`
	// A reference to the pipeline that was used for this decision.
	// This reference can be used to look up the pipeline definition and its
	// scheduler step configuration for additional context.
	PipelineRef corev1.ObjectReference `json:"pipelineRef"`
	// The Intent for this scheduling event.
	Intent SchedulingIntent `json:"intent"`
}

type DecisionSpec struct {
	// SchedulingDomain defines in which scheduling domain this decision
	// was or is processed (e.g., nova, cinder, manila).
	SchedulingDomain SchedulingDomain `json:"schedulingDomain"`

	// An identifier for the underlying resource to be scheduled.
	// For example, this can be the UUID of a nova instance or cinder volume.
	ResourceID string `json:"resourceID"`
}

const (
	// The decision is ready and tracking the resource.
	DecisionConditionReady = "Ready"
	// The decision has failed to make a placement decision for the resource.
	DecisionConditionFailed = "Failed"
)

type DecisionStatus struct {
	// The target host selected for the resource. Can be empty if no host could be determined.
	// +kubebuilder:validation:Optional
	TargetHost string `json:"targetHost,omitempty"`

	// The history of scheduling events for this resource.
	// +kubebuilder:validation:Optional
	SchedulingHistory []SchedulingHistoryEntry `json:"schedulingHistory,omitempty"`

	// A human-readable explanation of the current scheduling decision.
	// +kubebuilder:validation:Optional
	Explanation string `json:"explanation,omitempty"`

	// The current status conditions of the decision.
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

// Decision is the Schema for the decisions API
type Decision struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Decision
	// +required
	Spec DecisionSpec `json:"spec"`

	// status defines the observed state of Decision
	// +optional
	Status DecisionStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// DecisionList contains a list of Decision
type DecisionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Decision `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Decision{}, &DecisionList{})
}
