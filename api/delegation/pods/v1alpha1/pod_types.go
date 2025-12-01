// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Custom PodSpec with fields needed by the cortex
// scheduler that can be upstreamed later.
type PodSpec struct {
	corev1.PodSpec `json:",inline"`

	// The scheduler to use for this pod.
	//
	// If the scheduler is set to "cortex", the cortex scheduler
	// will assign a node if the node name is unset.
	Scheduler string `json:"scheduler,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:storageversion

// Pod is the Schema for the pods API
type Pod struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PodSpec          `json:"spec,omitempty"`
	Status corev1.PodStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PodList contains a list of Pod
type PodList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Pod `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Pod{}, &PodList{})
}
