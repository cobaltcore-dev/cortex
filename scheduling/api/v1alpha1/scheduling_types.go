// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// The type of scheduling.
type SchedulingType string

const (
	// The scheduling was created by the nova external scheduler call.
	// Usually we refer to this as nova initial placement, it also includes
	// migrations or resizes.
	SchedulingTypeNova SchedulingType = "nova"
	// The scheduling was created by the cinder external scheduler call.
	SchedulingTypeCinder SchedulingType = "cinder"
	// The scheduling was created by the manila external scheduler call.
	SchedulingTypeManila SchedulingType = "manila"
	// The scheduling was created by spawning an ironcore machine.
	SchedulingTypeMachine SchedulingType = "machine"
)

type SchedulingSpec struct {
	// The operator by which this scheduling should be extracted.
	Operator string `json:"operator,omitempty"`

	// When there is a source host for the scheduling, it is recorded here.
	//
	// Note: for initial placements, this will be empty. However, for migrations
	// or resizes, this will contain the source host.
	// +kubebuilder:validation:Optional
	SourceHost string `json:"sourceHost,omitempty"`

	// A reference to the pipeline that should be used for this scheduling.
	// This reference can be used to look up the pipeline definition and its
	// scheduler step configuration for additional context.
	PipelineRef corev1.ObjectReference `json:"pipelineRef"`

	// An identifier for the underlying resource to be scheduled.
	// For example, this can be the UUID of a nova instance or cinder volume.
	// This can be used to correlate multiple schedulings for the same resource.
	ResourceID string `json:"resourceID"`

	// The type of scheduling, indicating what has initiated this scheduling.
	Type SchedulingType `json:"type"`
	// If the type is "nova", this field contains the raw nova scheduling request.
	// +kubebuilder:validation:Optional
	NovaRaw *runtime.RawExtension `json:"novaRaw,omitempty"`
	// If the type is "cinder", this field contains the raw cinder scheduling request.
	// +kubebuilder:validation:Optional
	CinderRaw *runtime.RawExtension `json:"cinderRaw,omitempty"`
	// If the type is "manila", this field contains the raw manila scheduling request.
	// +kubebuilder:validation:Optional
	ManilaRaw *runtime.RawExtension `json:"manilaRaw,omitempty"`
	// If the type is "machine", this field contains the machine reference.
	// +kubebuilder:validation:Optional
	MachineRef *corev1.ObjectReference `json:"machineRef,omitempty"`
}

type SchedulingDecisionType string

const (
	// Scheduling decision for the nova external scheduler call.
	SchedulingDecisionTypeNova SchedulingDecisionType = "nova"
	// Scheduling decision for the cinder external scheduler call.
	SchedulingDecisionTypeCinder SchedulingDecisionType = "cinder"
	// Scheduling decision for the manila external scheduler call.
	SchedulingDecisionTypeManila SchedulingDecisionType = "manila"
	// Scheduling decision for an ironcore machine.
	SchedulingDecisionTypeMachine SchedulingDecisionType = "machine"
)

type NovaSchedulingDecision struct {
	// Sorted list of compute hosts from more preferred to least preferred.
	ComputeHosts []string `json:"computeHosts"`
	// Outputs of the scheduling pipeline including the activations used
	// to make the final ordering of compute hosts.
	Activations map[string]float64 `json:"activations,omitempty"`
}

type CinderSchedulingDecision struct {
	// Sorted list of storage hosts from more preferred to least preferred.
	StoragePools []string `json:"storagePools"`
	// Outputs of the scheduling pipeline including the activations used
	// to make the final ordering of storage hosts.
	Activations map[string]float64 `json:"activations,omitempty"`
}

type ManilaSchedulingDecision struct {
	// Sorted list of share hosts from more preferred to least preferred.
	StoragePools []string `json:"storagePools"`
	// Outputs of the scheduling pipeline including the activations used
	// to make the final ordering of share hosts.
	Activations map[string]float64 `json:"activations,omitempty"`
}

type MachineSchedulingDecision struct {
	// Sorted list of machine pools from more preferred to least preferred.
	MachinePools []string `json:"machinePools"`
	// Outputs of the scheduling pipeline including the activations used
	// to make the final ordering of machine pools.
	Activations map[string]float64 `json:"activations,omitempty"`
}

type SchedulingStatus struct {
	// The time it took to schedule.
	// +kubebuilder:validation:Optional
	Took metav1.Duration `json:"took"`

	// When there is a designated target host for the scheduling, it is recorded
	// here.
	//
	// Note: for external scheduler requests, this will be the first host from
	// the list of returned hosts -- meaning there is no guarantee this is
	// actually the host where the resource will be spawned on. Please check
	// the decision details to see the full list of hosts and their scores.
	//
	// For deschedulings, this will be empty, indicating there is no specific
	// target host.
	// +kubebuilder:validation:Optional
	TargetHost string `json:"targetHost,omitempty"`

	// The type of scheduling decision made.
	// +kubebuilder:validation:Optional
	DecisionType SchedulingDecisionType `json:"decisionType,omitempty"`
	// If the scheduling decision type is "nova", this field contains the
	// nova scheduling decision.
	// +kubebuilder:validation:Optional
	NovaDecision *NovaSchedulingDecision `json:"novaDecision,omitempty"`
	// If the scheduling decision type is "cinder", this field contains the
	// cinder scheduling decision.
	// +kubebuilder:validation:Optional
	CinderDecision *CinderSchedulingDecision `json:"cinderDecision,omitempty"`
	// If the scheduling decision type is "manila", this field contains the
	// manila scheduling decision.
	// +kubebuilder:validation:Optional
	ManilaDecision *ManilaSchedulingDecision `json:"manilaDecision,omitempty"`
	// If the scheduling decision type is "machine", this field contains the
	// machine scheduling decision.
	// +kubebuilder:validation:Optional
	MachineDecision *MachineSchedulingDecision `json:"machineDecision,omitempty"`

	// If there were previous schedulings for the underlying resource, they will
	// be resolved here to provide historical context for the scheduling.
	// +kubebuilder:validation:Optional
	History []corev1.ObjectReference `json:"history,omitempty"`

	// A human-readable explanation of the scheduling result.
	// +kubebuilder:validation:Optional
	Explanation string `json:"explanation,omitempty"`

	// If there was an error during the last scheduling, it is recorded here.
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

// Scheduling is the Schema for the schedulings API
type Scheduling struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Scheduling
	// +required
	Spec SchedulingSpec `json:"spec"`

	// status defines the observed state of Scheduling
	// +optional
	Status SchedulingStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// SchedulingList contains a list of Scheduling
type SchedulingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Scheduling `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Scheduling{}, &SchedulingList{})
}
