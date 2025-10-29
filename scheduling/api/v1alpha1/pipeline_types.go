// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type StepInPipeline struct {
	// Reference to the step.
	Ref corev1.ObjectReference `json:"ref"`
	// Whether this step is mandatory for the pipeline to be runnable.
	// +kubebuilder:default=true
	Mandatory bool `json:"mandatory"`
}

type PipelineType string

const (
	// Pipeline containing filter-weigher steps for initial placement,
	// migration, etc. of instances.
	PipelineTypeFilterWeigher PipelineType = "filter-weigher"
	// Pipeline containing descheduler steps for generating descheduling
	// recommendations.
	PipelineTypeDescheduler PipelineType = "descheduler"
)

type PipelineSpec struct {
	// The operator by which this pipeline should be handled.
	Operator string `json:"operator,omitempty"`
	// An optional description of the pipeline.
	// +kubebuilder:validation:Optional
	Description string `json:"description,omitempty"`
	// The type of the pipeline.
	Type PipelineType `json:"type"`
	// The ordered list of steps that make up this pipeline.
	Steps []StepInPipeline `json:"steps,omitempty"`
}

type PipelineStatus struct {
	// Whether the pipeline is ready to be used.
	Ready bool `json:"ready"`
	// The total number of steps configured in the pipeline.
	TotalSteps int `json:"totalSteps"`
	// The number of steps that are ready.
	ReadySteps int `json:"readySteps"`
	// An overview of the readiness of the steps in the pipeline.
	// Format: "ReadySteps / TotalSteps steps ready".
	StepsReadyFrac string `json:"stepsReadyFrac,omitempty"`
	// An error explaining why the pipeline is not ready, if applicable.
	Error string `json:"error,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Created",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Operator",type="string",JSONPath=".spec.operator"
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Ready",type="boolean",JSONPath=".status.ready"
// +kubebuilder:printcolumn:name="Steps",type="string",JSONPath=".status.stepsReadyFrac"
// +kubebuilder:printcolumn:name="Error",type="string",JSONPath=".status.error"

// Pipeline is the Schema for the decisions API
type Pipeline struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

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
