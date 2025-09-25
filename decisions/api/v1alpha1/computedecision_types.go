// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ComputeDecisionPipelineOutputSpec struct {
	Step    string             `json:"step"`
	Weights map[string]float64 `json:"weights,omitempty"`
}

type ComputeDecisionPipelineSpec struct {
	Name    string                              `json:"name"`
	Outputs []ComputeDecisionPipelineOutputSpec `json:"outputs,omitempty"`
}

// ComputeDecisionSpec defines the desired state of ComputeDecision.
type ComputeDecisionSpec struct {
	Pipeline ComputeDecisionPipelineSpec `json:"pipeline"`
}

type ComputeDecisionFactorStatus struct {
	Host string `json:"host"`
	Expl string `json:"expl"`
}

// ComputeDecisionStatus defines the observed state of ComputeDecision.
type ComputeDecisionStatus struct {
	Description string                        `json:"description,omitempty"`
	Factors     []ComputeDecisionFactorStatus `json:"factors,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=cdec

// ComputeDecision is the Schema for the computedecisions API
type ComputeDecision struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of ComputeDecision
	// +required
	Spec ComputeDecisionSpec `json:"spec"`

	// status defines the observed state of ComputeDecision
	// +optional
	Status ComputeDecisionStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ComputeDecisionList contains a list of ComputeDecision
type ComputeDecisionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ComputeDecision `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ComputeDecision{}, &ComputeDecisionList{})
}
