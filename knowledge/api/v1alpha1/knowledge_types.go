// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type KnowledgeSpec struct{}

type KnowledgeStatus struct{}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Created",type="date",JSONPath=".metadata.creationTimestamp"

// Knowledge is the Schema for the knowledges API
type Knowledge struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Knowledge
	// +required
	Spec KnowledgeSpec `json:"spec"`

	// status defines the observed state of Knowledge
	// +optional
	Status KnowledgeStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// KnowledgeList contains a list of Knowledge
type KnowledgeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Knowledge `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Knowledge{}, &KnowledgeList{})
}
