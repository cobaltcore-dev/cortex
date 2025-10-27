// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

type DisabledValidationsSpec struct {
	// Whether to validate that no subjects are removed or added from the scheduler
	// step. This should only be disabled for scheduler steps that remove subjects.
	// Thus, if no value is provided, the default is false.
	SameSubjectNumberInOut bool `json:"sameSubjectNumberInOut,omitempty"`
	// Whether to validate that, after running the step, there are remaining subjects.
	// This should only be disabled for scheduler steps that are expected to
	// remove all subjects.
	SomeSubjectsRemain bool `json:"someSubjectsRemain,omitempty"`
}

type StepType string

const (
	// Step for assigning weights to hosts.
	StepTypeFilterWeigher StepType = "weigher"
	// Step for filtering hosts.
	StepTypeFilter StepType = "filter"
	// Step for generating descheduling recommendations.
	StepTypeDescheduler StepType = "descheduler"
)

type WeigherSpec struct {
	// The validations to disable for this step. If none are provided, all
	// applied validations are enabled.
	// +kubebuilder:validation:Optional
	DisabledValidations DisabledValidationsSpec `json:"disabledValidations,omitempty"`
}

type DeschedulerSpec struct {
	// If dry-run is disabled (by default its enabled).
	// +kubebuilder:validation:Optional
	DisableDryRun bool `json:"disableDryRun,omitempty"`
}

type StepSpec struct {
	// The type of the scheduler step.
	Type StepType `json:"type"`
	// If the type is "weigher", this contains additional configuration for it.
	// +kubebuilder:validation:Optional
	Weigher *WeigherSpec `json:"weigher,omitempty"`
	// If the type is "descheduler", this contains additional configuration for it.
	// +kubebuilder:validation:Optional
	Descheduler *DeschedulerSpec `json:"descheduler,omitempty"`

	// The name of the scheduler step in the cortex implementation.
	Impl string `json:"impl"`
	// Additional configuration for the extractor that can be used
	// +kubebuilder:validation:Optional
	Opts runtime.RawExtension `json:"opts,omitempty"`
	// Knowledges this step depends on to be ready.
	// +kubebuilder:validation:Optional
	Knowledges []corev1.ObjectReference `json:"knowledges,omitempty"`
	// Additional description of the step which helps understand its purpose
	// and decisions made by it.
	// +kubebuilder:validation:Optional
	Description string `json:"description,omitempty"`
}

type StepStatus struct {
	// If the step is ready to be executed.
	Ready bool `json:"ready"`
	// How many knowledges have been extracted.
	ReadyKnowledges int `json:"readyKnowledges"`
	// Total number of knowledges configured.
	TotalKnowledges int `json:"totalKnowledges"`
	// "ReadyKnowledges / TotalKnowledges ready" as a human-readable string
	// or "ready" if there are no knowledges configured.
	KnowledgesReady string `json:"knowledgesReady,omitempty"`
	// An error explaining why the step is not ready, if applicable.
	Error string `json:"error,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Created",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Operator",type="string",JSONPath=".spec.operator"
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Ready",type="boolean",JSONPath=".status.ready"
// +kubebuilder:printcolumn:name="Knowledges Ready",type="string",JSONPath=".status.knowledgesReady"
// +kubebuilder:printcolumn:name="Error",type="string",JSONPath=".status.error"

// Step is the Schema for the deschedulings API
type Step struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Step
	// +required
	Spec StepSpec `json:"spec"`

	// status defines the observed state of Step
	// +optional
	Status StepStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// StepList contains a list of Step
type StepList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Step `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Step{}, &StepList{})
}
