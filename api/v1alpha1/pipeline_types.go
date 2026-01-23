// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

type StepSpec struct {
	// The name of the scheduler step in the cortex implementation.
	// Must match to a step implemented by the pipeline controller.
	Name string `json:"name"`

	// Additional configuration for the step that can be used
	// +kubebuilder:validation:Optional
	Opts runtime.RawExtension `json:"opts,omitempty"`

	// Additional description of the step which helps understand its purpose
	// and decisions made by it.
	// +kubebuilder:validation:Optional
	Description string `json:"description,omitempty"`

	// If required, steps can specify knowledges on which they depend.
	// Changes to the knowledges' readiness will trigger re-evaluation of
	// pipelines containing this step.
	// +kubebuilder:validation:Optional
	Knowledges []corev1.ObjectReference `json:"knowledges,omitempty"`
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
	// SchedulingDomain defines in which scheduling domain this pipeline
	// is used (e.g., nova, cinder, manila).
	SchedulingDomain SchedulingDomain `json:"schedulingDomain"`

	// An optional description of the pipeline, helping understand its purpose.
	// +kubebuilder:validation:Optional
	Description string `json:"description,omitempty"`

	// If this pipeline should create decision objects.
	// When this is false, the pipeline will still process requests.
	// +kubebuilder:default=false
	CreateDecisions bool `json:"createDecisions,omitempty"`

	// The type of the pipeline, used to differentiate between
	// filter-weigher and descheduler pipelines within the same
	// scheduling domain.
	//
	// If the type is filter-weigher, the filter and weigher attributes
	// must be set. If the type is descheduler, the detectors attribute
	// must be set.
	//
	// +kubebuilder:validation:Enum=filter-weigher;descheduler
	Type PipelineType `json:"type"`

	// Ordered list of filters to apply in a scheduling pipeline.
	//
	// This attribute is set only if the pipeline type is filter-weigher.
	// Filters remove host candidates from an initial set, leaving
	// valid candidates. Filters are run before weighers are applied.
	// +kubebuilder:validation:Optional
	Filters []StepSpec `json:"filters,omitempty"`

	// Ordered list of weighers to apply in a scheduling pipeline.
	//
	// This attribute is set only if the pipeline type is filter-weigher.
	// These weighers are run after filters are applied.
	// +kubebuilder:validation:Optional
	Weighers []StepSpec `json:"weighers,omitempty"`

	// Ordered list of detectors to apply in a descheduling pipeline.
	//
	// This attribute is set only if the pipeline type is descheduler.
	// Detectors find candidates for descheduling (migration off current host).
	// These detectors are run after weighers are applied.
	// +kubebuilder:validation:Optional
	Detectors []StepSpec `json:"detectors,omitempty"`
}

const (
	// The pipeline is ready to be used.
	PipelineConditionReady = "Ready"
)

type PipelineStatus struct {
	// The total number of steps configured in the pipeline.
	TotalSteps int `json:"totalSteps"`
	// The number of steps that are ready.
	ReadySteps int `json:"readySteps"`
	// An overview of the readiness of the steps in the pipeline.
	// Format: "ReadySteps / TotalSteps steps ready".
	StepsReadyFrac string `json:"stepsReadyFrac,omitempty"`
	// The current status conditions of the pipeline.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Created",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Domain",type="string",JSONPath=".spec.schedulingDomain"
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Steps",type="string",JSONPath=".status.stepsReadyFrac"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"

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

func (*Pipeline) URI() string     { return "pipelines.cortex.cloud/v1alpha1" }
func (*PipelineList) URI() string { return "pipelines.cortex.cloud/v1alpha1" }

func init() {
	SchemeBuilder.Register(&Pipeline{}, &PipelineList{})
}
