// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	computev1alpha1 "github.com/ironcore-dev/ironcore/api/compute/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:storageversion

// Machine is the Schema for the machines API
type MachinePool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   computev1alpha1.MachinePoolSpec   `json:"spec,omitempty"`
	Status computev1alpha1.MachinePoolStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MachineList contains a list of Machine
type MachinePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MachinePool `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MachinePool{}, &MachinePoolList{})
}
