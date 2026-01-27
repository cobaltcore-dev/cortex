// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type PodGroupSpec struct {
	Replicas int32          `json:"replicas"`
	PodSpec  corev1.PodSpec `json:"podSpec"`
}

type PodGroup struct {
	Name string       `json:"name"`
	Spec PodGroupSpec `json:"spec"`
}

type PodGroupSetSpec struct {
	PodGroups []PodGroup `json:"podGroups"`
}

type PodGroupSetStatus struct {
	// The current status conditions of the pod group set.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced

// PodGroupSet is the Schema for the podgroupsets API
type PodGroupSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PodGroupSetSpec   `json:"spec,omitempty"`
	Status PodGroupSetStatus `json:"status,omitempty"`
}

// PodGroupSet.PodName constructs a unique identifier for its pods that is used
// in the mapping of potential placements.
func (pgs PodGroupSet) PodName(podGroupName string, replicaIdx int) string {
	return fmt.Sprintf("%s-%s-%d", pgs.Name, podGroupName, replicaIdx)
}

// +kubebuilder:object:root=true

// PodGroupSetList contains a list of PodGroupSet
type PodGroupSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PodGroupSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PodGroupSet{}, &PodGroupSetList{})
}
