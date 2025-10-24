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
	DecisionTypeNova DecisionType = "nova"
	// The decision was created by the cinder external scheduler call.
	DecisionTypeCinder DecisionType = "cinder"
	// The decision was created by the manila external scheduler call.
	DecisionTypeManila DecisionType = "manila"
	// The decision was created by spawning an ironcore machine.
	DecisionTypeMachine DecisionType = "machine"
)

type DecisionSpec struct {
	// The operator by which this decision should be extracted.
	Operator string `json:"operator,omitempty"`

	// If there were previous decisions for the underlying resource, they can
	// be provided here to provide historical context for the decision.
	// +kubebuilder:validation:Optional
	History []corev1.ObjectReference `json:"history,omitempty"`

	// When there is a source host for the decision, it is recorded here.
	//
	// Note: for initial placements, this will be empty. However, for migrations
	// or resizes, this will contain the source host.
	// +kubebuilder:validation:Optional
	SourceHost string `json:"sourceHost,omitempty"`

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
	// Name of the scheduler step.
	StepName string `json:"stepName"`
	// Activations of the step for each host.
	Activations map[string]float64 `json:"activations"`
}

type NovaDecision struct {
	// Sorted list of compute hosts from more preferred to least preferred.
	ComputeHosts []string `json:"computeHosts"`
	// Outputs of the decision pipeline including the activations used
	// to make the final ordering of compute hosts.
	StepResults []StepResult `json:"stepResults,omitempty"`
}

type CinderDecision struct {
	// Sorted list of storage hosts from more preferred to least preferred.
	StoragePools []string `json:"storagePools"`
	// Outputs of the decision pipeline including the activations used
	// to make the final ordering of storage hosts.
	StepResults []StepResult `json:"stepResults,omitempty"`
}

type ManilaDecision struct {
	// Sorted list of share hosts from more preferred to least preferred.
	StoragePools []string `json:"storagePools"`
	// Outputs of the decision pipeline including the activations used
	// to make the final ordering of share hosts.
	StepResults []StepResult `json:"stepResults,omitempty"`
}

type MachineDecision struct {
	// Sorted list of machine pools from more preferred to least preferred.
	MachinePools []string `json:"machinePools"`
	// Outputs of the decision pipeline including the activations used
	// to make the final ordering of machine pools.
	StepResults []StepResult `json:"stepResults,omitempty"`
}

type DecisionStatus struct {
	// The time it took to schedule.
	// +kubebuilder:validation:Optional
	Took metav1.Duration `json:"took"`

	// When there is a designated target host for the decision, it is recorded
	// here.
	//
	// Note: for external scheduler requests, this will be the first host from
	// the list of returned hosts -- meaning there is no guarantee this is
	// actually the host where the resource will be spawned on. Please check
	// the decision details to see the full list of hosts and their scores.
	//
	// For dedecisions, this will be empty, indicating there is no specific
	// target host.
	// +kubebuilder:validation:Optional
	TargetHost string `json:"targetHost,omitempty"`

	// If the decision decision type is "nova", this field contains the
	// nova decision decision.
	// +kubebuilder:validation:Optional
	Nova *NovaDecision `json:"nova,omitempty"`
	// If the decision decision type is "cinder", this field contains the
	// cinder decision decision.
	// +kubebuilder:validation:Optional
	Cinder *CinderDecision `json:"cinder,omitempty"`
	// If the decision decision type is "manila", this field contains the
	// manila decision decision.
	// +kubebuilder:validation:Optional
	Manila *ManilaDecision `json:"manila,omitempty"`
	// If the decision decision type is "machine", this field contains the
	// machine decision decision.
	// +kubebuilder:validation:Optional
	Machine *MachineDecision `json:"machine,omitempty"`

	// A human-readable explanation of the decision result.
	// +kubebuilder:validation:Optional
	Explanation string `json:"explanation,omitempty"`

	// If there was an error during the last decision, it is recorded here.
	// +kubebuilder:validation:Optional
	Error string `json:"error,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Operator",type="string",JSONPath=".spec.operator"
// +kubebuilder:printcolumn:name="Created",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Took",type="string",JSONPath=".status.took"
// +kubebuilder:printcolumn:name="Error",type="string",JSONPath=".status.error"

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
