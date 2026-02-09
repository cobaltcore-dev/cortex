// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

type DecisionSpec struct {
	// Reference to the resource being scheduled (e.g., VM, Pod, Machine).
	// This will typically reference the VM CRD in the future.
	ResourceRef corev1.ObjectReference `json:"resourceRef"`

	// DEPRECATED: Transition field - will be moved to VM CRD.
	// If the type is "nova", this field contains the raw nova decision request.
	// +kubebuilder:validation:Optional
	NovaRaw *runtime.RawExtension `json:"novaRaw,omitempty"`
}

// PlacementRecord tracks a single placement decision for a resource.
// This is used to build a history of where a resource has been scheduled
// and when, enabling migration rate limiting and cycle prevention.
type PlacementRecord struct {
	// The host where the resource was placed.
	Host string `json:"host"`
	// When this placement decision was made.
	Timestamp metav1.Time `json:"timestamp"`
	// The reason for this placement (e.g., "InitialPlacement", "Migration", "Rescheduling", "Descheduling").
	Reason string `json:"reason"`
	// Optional reference to the pipeline that made this decision.
	// +kubebuilder:validation:Optional
	PipelineRef *corev1.ObjectReference `json:"pipelineRef,omitempty"`
}

const (
	// The decision was successfully processed.
	DecisionConditionReady = "Ready"
	// The resource could not be scheduled to any host.
	DecisionConditionUnschedulable = "Unschedulable"
)

type DecisionStatus struct {
	// PlacementHistory tracks the last N placement decisions for this resource.
	// This provides a data basis for migration rate limiting and cycle prevention.
	// Detailed explanations are communicated via Kubernetes Events.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=50
	PlacementHistory []PlacementRecord `json:"placementHistory,omitempty"`

	// CurrentHost is the current/target host for the resource.
	// +kubebuilder:validation:Optional
	CurrentHost *string `json:"currentHost,omitempty"`

	// The current status conditions of the decision.
	// Use events (kubectl describe) for detailed scheduling explanations.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Resource",type="string",JSONPath=".spec.resourceRef.name"
// +kubebuilder:printcolumn:name="CurrentHost",type="string",JSONPath=".status.currentHost"
// +kubebuilder:printcolumn:name="Placements",type="integer",JSONPath=".status.placementHistory[*].host"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"

// Decision tracks placement decisions and history for scheduled resources.
// It provides transparency (via events) and a data basis (via placement history)
// for scheduling decisions, migration rate limiting, and cycle prevention.
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
