// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type KnowledgeSpec struct {
}

type KnowledgeStatus struct {
	// Conditions reflects current status conditions of the knowledge.
	// +kubebuilder:validation:Optional
	Conditions []KnowledgeCondition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// KnowledgeCondition ...
type KnowledgeCondition struct {
	// Type of the condition.
	Type KnowledgeConditionType `json:"type"`

	// Status of the condition.
	Status metav1.ConditionStatus `json:"status"`

	// LastTransitionTime is the timestamp corresponding to the last status change of this condition.
	// +optional
	LastTransitionTime *metav1.Time `json:"lastTransitionTime,omitempty"`

	// Reason is a brief machine-readable explanation for the condition's last transition.
	// +optional
	Reason string `json:"reason,omitempty"`

	// Message is a human-readable description of the details of the last transition.
	// +optional
	Message string `json:"message,omitempty"`
}

// KnowledgeConditionType represents a Knowledge condition value.
type KnowledgeConditionType string

const (
	// KnowledgeConditionTypeReady indicates that a Knowledge is ready.
	KnowledgeConditionTypeReady KnowledgeConditionType = "Ready"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Created",type="date",JSONPath=".metadata.creationTimestamp"

// Knowledge is the Schema for the knowledges API
type Knowledge struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of Knowledge
	// +required
	Spec KnowledgeSpec `json:"spec"`

	// status defines the observed state of Knowledge
	// +optional
	Status KnowledgeStatus `json:"status,omitempty"`
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
