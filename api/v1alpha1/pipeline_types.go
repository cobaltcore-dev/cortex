// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type FilterSpec struct {
	// The name of the scheduler step in the cortex implementation.
	// Must match to a step implemented by the pipeline controller.
	Name string `json:"name"`

	// Additional configuration for the step that can be used
	// +kubebuilder:validation:Optional
	Params Parameters `json:"params,omitempty"`

	// Additional description of the step which helps understand its purpose
	// and decisions made by it.
	// +kubebuilder:validation:Optional
	Description string `json:"description,omitempty"`
}

type WeigherSpec struct {
	// The name of the scheduler step in the cortex implementation.
	// Must match to a step implemented by the pipeline controller.
	Name string `json:"name"`

	// Additional configuration for the step that can be used
	// +kubebuilder:validation:Optional
	Params Parameters `json:"params,omitempty"`

	// Additional description of the step which helps understand its purpose
	// and decisions made by it.
	// +kubebuilder:validation:Optional
	Description string `json:"description,omitempty"`

	// Optional multiplier to apply to the step's output.
	// This can be used to increase or decrease the weight of a step
	// relative to other steps in the same pipeline.
	// +kubebuilder:validation:Optional
	Multiplier *float64 `json:"multiplier,omitempty"`
}

type DetectorSpec struct {
	// The name of the scheduler step in the cortex implementation.
	// Must match to a step implemented by the pipeline controller.
	Name string `json:"name"`

	// Additional configuration for the step that can be used
	// +kubebuilder:validation:Optional
	Params Parameters `json:"params,omitempty"`

	// Additional description of the step which helps understand its purpose
	// and decisions made by it.
	// +kubebuilder:validation:Optional
	Description string `json:"description,omitempty"`
}

type PipelineType string

const (
	// Pipeline containing filter-weigher steps for initial placement,
	// migration, etc. of instances.
	PipelineTypeFilterWeigher PipelineType = "filter-weigher"
	// Pipeline containing detector steps, e.g. for generating descheduling
	// recommendations.
	PipelineTypeDetector PipelineType = "detector"
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

	// If this pipeline should ignore host preselection and gather all
	// available placement candidates before applying filters, instead of
	// relying on a pre-filtered set and weights.
	// +kubebuilder:default=false
	IgnorePreselection bool `json:"ignorePreselection,omitempty"`

	// The type of the pipeline, used to differentiate between
	// filter-weigher and detector pipelines within the same
	// scheduling domain.
	//
	// If the type is filter-weigher, the filter and weigher attributes
	// must be set. If the type is detector, the detectors attribute
	// must be set.
	//
	// +kubebuilder:validation:Enum=filter-weigher;detector
	Type PipelineType `json:"type"`

	// Ordered list of filters to apply in a scheduling pipeline.
	//
	// This attribute is set only if the pipeline type is filter-weigher.
	// Filters remove host candidates from an initial set, leaving
	// valid candidates. Filters are run before weighers are applied.
	// +kubebuilder:validation:Optional
	Filters []FilterSpec `json:"filters,omitempty"`

	// Ordered list of weighers to apply in a scheduling pipeline.
	//
	// This attribute is set only if the pipeline type is filter-weigher.
	// These weighers are run after filters are applied.
	// +kubebuilder:validation:Optional
	Weighers []WeigherSpec `json:"weighers,omitempty"`

	// Ordered list of detectors to apply in a descheduling pipeline.
	//
	// This attribute is set only if the pipeline type is detector.
	// Detectors find candidates for descheduling (migration off current host).
	// These detectors are run after weighers are applied.
	// +kubebuilder:validation:Optional
	Detectors []DetectorSpec `json:"detectors,omitempty"`
}

const (
	// The pipeline is ready to be used.
	PipelineConditionReady = "Ready"
	// All steps in the pipeline are ready.
	PipelineConditionAllStepsReady = "AllStepsReady"
	// All of the steps in the pipeline are indexed (known by the controller).
	PipelineConditionAllStepsIndexed = "AllStepsIndexed"
)

type PipelineStatus struct {
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
// +kubebuilder:printcolumn:name="All Steps Ready",type="string",JSONPath=".status.conditions[?(@.type=='AllStepsReady')].status"
// +kubebuilder:printcolumn:name="All Steps Known",type="string",JSONPath=".status.conditions[?(@.type=='AllStepsIndexed')].status"
// +kubebuilder:printcolumn:name="Pipeline Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"

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
