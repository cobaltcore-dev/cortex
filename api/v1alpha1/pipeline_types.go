// Copyright SAP SE
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
	StepTypeWeigher StepType = "weigher"
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

type StepSpec struct {
	// The type of the scheduler step.
	Type StepType `json:"type"`
	// If the type is "weigher", this contains additional configuration for it.
	// +kubebuilder:validation:Optional
	Weigher *WeigherSpec `json:"weigher,omitempty"`

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
	// Pipeline containing gang scheduling logic.
	PipelineTypeGang PipelineType = "gang"
)

type PipelineSpec struct {
	// SchedulingDomain defines in which scheduling domain this pipeline
	// is used (e.g., nova, cinder, manila).
	SchedulingDomain SchedulingDomain `json:"schedulingDomain"`
	// An optional description of the pipeline.
	// +kubebuilder:validation:Optional
	Description string `json:"description,omitempty"`
	// If this pipeline should create decision objects.
	// When this is false, the pipeline will still process requests.
	// +kubebuilder:default=false
	CreateDecisions bool `json:"createDecisions,omitempty"`
	// The type of the pipeline.
	Type PipelineType `json:"type"`
	// The ordered list of steps that make up this pipeline.
	Steps []StepSpec `json:"steps,omitempty"`
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
