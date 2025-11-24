// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type (
	// FilterSpec is a hard constraints to ensure valid placement and scheduling and must be executed.
	FilterSpec struct {
		// Name ...
		Name string `json:"name"`

		// Description ...
		Description string `json:"description,omitempty"`

		// Params ...
		Params map[string]string `json:"params,omitempty"`
	}

	FilterStatus struct {
		// Name ...
		Name string `json:"name"`

		Status string `json:"status"`
	}
)

type (
	// WeigherSpec is a scheduling objective and should be executed to achieve optimal placement and scheduling.
	WeigherSpec struct {
		// Name ...
		Name string `json:"name"`

		// Description ...
		Description string `json:"description,omitempty"`

		// Params ...
		Params map[string]string `json:"params,omitempty"`

		// Multiplier ...
		Multiplier *float64 `json:"multiplier,omitempty"`
	}

	WeigherStatus struct {
		// Name ...
		Name string `json:"name"`

		Status string `json:"status"`
	}
)

type PipelineSpec struct {
	// Filters ...
	Filters []FilterSpec `json:"filters,omitempty"`

	// Weighers ...
	Weighers []WeigherSpec `json:"weighers,omitempty"`
}

type PipelineConditionType string

const (
	// PipelineReady reflects the ready status of the pipeline.
	PipelineReady PipelineConditionType = "Ready"
)

type PipelineCondition struct {
	// Type of pipelne condition.
	Type PipelineConditionType `json:"type"`
	// Status of the condition, one of True, False, Unknown.
	Status metav1.ConditionStatus `json:"status"`
	// Last time we got an update on a given condition.
	// +optional
	LastHeartbeatTime metav1.Time `json:"lastHeartbeatTime,omitempty"`
	// Last time the condition transit from one status to another.
	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
	// (brief) reason for the condition's last transition.
	// +optional
	Reason string `json:"reason,omitempty"`
	// Human-readable message indicating details about last transition.
	// +optional
	Message string `json:"message,omitempty"`
}

type PipelineStatus struct {
	Filters []FilterStatus `json:"filters,omitempty"`

	Weighers []WeigherStatus `json:"weighers,omitempty"`

	// Conditions describe the status of the pipeline.
	// +kubebuilder:validation:Optional
	Conditions []PipelineCondition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Created",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Operator",type="string",JSONPath=".spec.operator"
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Ready",type="boolean",JSONPath=".status.ready"
// +kubebuilder:printcolumn:name="Steps",type="string",JSONPath=".status.stepsReadyFrac"

// Pipeline is the Schema for the decisions API
type Pipeline struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of Pipeline
	// +required
	Spec PipelineSpec `json:"spec"`

	// status defines the observed state of Pipeline
	// +optional
	Status PipelineStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// PipelineList contains a list of Pipeline
type PipelineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Pipeline `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Pipeline{}, &PipelineList{})
}
