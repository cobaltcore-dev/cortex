// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// The type of decision.
type DecisionType string

const (
	// The decision was created by the nova external scheduler call.
	// Usually we refer to this as nova initial placement, it also includes
	// migrations or resizes.
	DecisionTypeNovaServer DecisionType = "nova-server"
	// The decision was created by the cinder external scheduler call.
	DecisionTypeCinderVolume DecisionType = "cinder-volume"
	// The decision was created by the manila external scheduler call.
	DecisionTypeManilaShare DecisionType = "manila-share"
	// The decision was created by spawning an ironcore machine.
	DecisionTypeIroncoreMachine DecisionType = "ironcore-machine"
)

type DecisionSpec struct {
	// The operator by which this decision should be extracted.
	Operator string `json:"operator,omitempty"`

	// A reference to the pipeline that should be used for this decision.
	// This reference can be used to look up the pipeline definition and its
	// scheduler step configuration for additional context.
	PipelineRef corev1.ObjectReference `json:"pipelineRef"`

	// An identifier for the underlying resource to be scheduled.
	// For example, this can be the UUID of a nova instance or cinder volume.
	// This can be used to correlate multiple decisions for the same resource.
	ResourceID string `json:"resourceID"`

	// The type of decision, indicating what has initiated this decision.
	Type DecisionType `json:"type"`
	// If the type is "nova", this field contains the raw nova decision request.
	// +kubebuilder:validation:Optional
	NovaRaw *runtime.RawExtension `json:"novaRaw,omitempty"`
	// If the type is "cinder", this field contains the raw cinder decision request.
	// +kubebuilder:validation:Optional
	CinderRaw *runtime.RawExtension `json:"cinderRaw,omitempty"`
	// If the type is "manila", this field contains the raw manila decision request.
	// +kubebuilder:validation:Optional
	ManilaRaw *runtime.RawExtension `json:"manilaRaw,omitempty"`
	// If the type is "machine", this field contains the machine reference.
	// +kubebuilder:validation:Optional
	MachineRef *corev1.ObjectReference `json:"machineRef,omitempty"`
}

type StepResult struct {
	// object reference to the scheduler step.
	StepRef corev1.ObjectReference `json:"stepRef"`
	// Activations of the step for each host.
	Activations map[string]float64 `json:"activations"`
}

type DecisionResult struct {
	// Raw input weights to the pipeline.
	// +kubebuilder:validation:Optional
	RawInWeights map[string]float64 `json:"rawInWeights"`
	// Normalized input weights to the pipeline.
	// +kubebuilder:validation:Optional
	NormalizedInWeights map[string]float64 `json:"normalizedInWeights"`
	// Outputs of the decision pipeline including the activations used
	// to make the final ordering of compute hosts.
	// +kubebuilder:validation:Optional
	StepResults []StepResult `json:"stepResults,omitempty"`
	// Aggregated output weights from the pipeline.
	// +kubebuilder:validation:Optional
	AggregatedOutWeights map[string]float64 `json:"aggregatedOutWeights"`
	// Final ordered list of hosts from most preferred to least preferred.
	// +kubebuilder:validation:Optional
	OrderedHosts []string `json:"orderedHosts,omitempty"`
	// The first element of the ordered hosts is considered the target host.
	// +kubebuilder:validation:Optional
	TargetHost *string `json:"targetHost,omitempty"`
}

const (
	// Something went wrong during the calculation of the decision.
	DecisionConditionError = "Error"
)

type DecisionStatus struct {
	// The time it took to schedule.
	// +kubebuilder:validation:Optional
	Took metav1.Duration `json:"took"`

	// The result of this decision.
	// +kubebuilder:validation:Optional
	Result *DecisionResult `json:"result,omitempty"`

	// If there were previous decisions for the underlying resource, they can
	// be resolved here to provide historical context for the decision.
	// +kubebuilder:validation:Optional
	History *[]corev1.ObjectReference `json:"history,omitempty"`

	// The number of decisions that preceded this one for the same resource.
	// +kubebuilder:validation:Optional
	Precedence *int `json:"precedence,omitempty"`

	// A human-readable explanation of the decision result.
	// +kubebuilder:validation:Optional
	Explanation string `json:"explanation,omitempty"`

	// The current status conditions of the decision.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Operator",type="string",JSONPath=".spec.operator"
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Resource ID",type="string",JSONPath=".spec.resourceID"
// +kubebuilder:printcolumn:name="#",type="string",JSONPath=".status.precedence"
// +kubebuilder:printcolumn:name="Created",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Took",type="string",JSONPath=".status.took"
// +kubebuilder:printcolumn:name="Pipeline",type="string",JSONPath=".spec.pipelineRef.name"
// +kubebuilder:printcolumn:name="TargetHost",type="string",JSONPath=".status.result.targetHost"
// +kubebuilder:selectablefield:JSONPath=".spec.resourceID"

// Decision is the Schema for the decisions API
type Decision struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Decision
	// +required
	Spec DecisionSpec `json:"spec"`

	// status defines the observed state of Decision
	// +optional
	Status DecisionStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// DecisionList contains a list of Decision
type DecisionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Decision `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Decision{}, &DecisionList{})
}
