// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// Dependencies required for extracting the kpi.
// If provided, all datasources and knowledges must have the same
// database secret reference so the kpi can be joined across multiple
// database tables.
type KPIDependenciesSpec struct {
	// Datasources required for extracting this kpi.
	// +kubebuilder:validation:Optional
	Datasources []corev1.ObjectReference `json:"datasources,omitempty"`

	// Knowledges this kpi depends on.
	// +kubebuilder:validation:Optional
	Knowledges []corev1.ObjectReference `json:"knowledges,omitempty"`
}

type KPISpec struct {
	// The operator by which this kpi should be executed.
	Operator string `json:"operator,omitempty"`

	// The name of the kpi in the cortex implementation.
	Impl string `json:"impl"`
	// Additional configuration for the extractor that can be used
	// +kubebuilder:validation:Optional
	Opts runtime.RawExtension `json:"opts,omitempty"`
	// Dependencies required for extracting this kpi.
	// +kubebuilder:validation:Optional
	Dependencies KPIDependenciesSpec `json:"dependencies"`
	// Additional description of the kpi which helps understand its purpose
	// and decisions made by it.
	// +kubebuilder:validation:Optional
	Description string `json:"description,omitempty"`
}

const (
	// Something went wrong during the kpi reconciliation.
	KPIConditionError = "Error"
)

type KPIStatus struct {
	// If the kpi is ready to be executed.
	Ready bool `json:"ready"`

	// How many dependencies have been reconciled.
	ReadyDependencies int `json:"readyDependencies"`
	// Total number of dependencies configured.
	TotalDependencies int `json:"totalDependencies"`
	// "ReadyDependencies / TotalDependencies ready" as a human-readable string
	// or "ready" if there are no dependencies configured.
	DependenciesReadyFrac string `json:"dependenciesReadyFrac,omitempty"`

	// The current status conditions of the kpi.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Created",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Operator",type="string",JSONPath=".spec.operator"
// +kubebuilder:printcolumn:name="Ready",type="boolean",JSONPath=".status.ready"
// +kubebuilder:printcolumn:name="Dependencies",type="string",JSONPath=".status.dependenciesReadyFrac"

// KPI is the Schema for the deschedulings API
type KPI struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of KPI
	// +required
	Spec KPISpec `json:"spec"`

	// status defines the observed state of KPI
	// +optional
	Status KPIStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// KPIList contains a list of KPI
type KPIList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KPI `json:"items"`
}

func (*KPI) URI() string     { return "kpis.cortex.cloud/v1alpha1" }
func (*KPIList) URI() string { return "kpis.cortex.cloud/v1alpha1" }

func init() {
	SchemeBuilder.Register(&KPI{}, &KPIList{})
}
