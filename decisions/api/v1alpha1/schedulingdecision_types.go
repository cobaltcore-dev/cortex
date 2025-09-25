// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SchedulingDecisionPipelineOutputSpec struct {
	Step string `json:"step"`
	// Weights calculated by this step subjected to the activation function.
	Activations map[string]float64 `json:"activations,omitempty"`
}

type SchedulingDecisionPipelineSpec struct {
	Name    string                                 `json:"name"`
	Outputs []SchedulingDecisionPipelineOutputSpec `json:"outputs,omitempty"`
}

// SchedulingDecisionSpec defines the desired state of SchedulingDecision.
type SchedulingDecisionSpec struct {
	Input    map[string]float64             `json:"input,omitempty"`
	Pipeline SchedulingDecisionPipelineSpec `json:"pipeline"`
}

type SchedulingDecisionState string

const (
	SchedulingDecisionStateResolved SchedulingDecisionState = "resolved"
	SchedulingDecisionStateError    SchedulingDecisionState = "error"
)

// SchedulingDecisionStatus defines the observed state of SchedulingDecision.
type SchedulingDecisionStatus struct {
	State SchedulingDecisionState `json:"state,omitempty"`
	// Only given if state is "error".
	Error       string `json:"error,omitempty"`
	Description string `json:"description,omitempty"`
	// Final scores for each host after processing all pipeline steps.
	FinalScores map[string]float64 `json:"finalScores,omitempty"`
	// Hosts that were deleted during pipeline processing and all steps that attempted to delete them.
	DeletedHosts map[string][]string `json:"deletedHosts,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=sdec;sdecs
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state"
// +kubebuilder:printcolumn:name="Error",type="string",JSONPath=".status.error"
// +kubebuilder:printcolumn:name="Created",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Description",type="string",JSONPath=".status.description"

// SchedulingDecision is the Schema for the schedulingdecisions API
type SchedulingDecision struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of SchedulingDecision
	// +required
	Spec SchedulingDecisionSpec `json:"spec"`

	// status defines the observed state of SchedulingDecision
	// +optional
	Status SchedulingDecisionStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// SchedulingDecisionList contains a list of SchedulingDecision
type SchedulingDecisionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SchedulingDecision `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SchedulingDecision{}, &SchedulingDecisionList{})
}
