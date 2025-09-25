// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SchedulingDecisionPipelineOutputSpec struct {
	Step    string             `json:"step"`
	Weights map[string]float64 `json:"weights,omitempty"`
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

// SchedulingDecisionStatus defines the observed state of SchedulingDecision.
type SchedulingDecisionStatus struct {
	Description string `json:"description,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=sdecs

// SchedulingDecision is the Schema for the computedecisions API
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
