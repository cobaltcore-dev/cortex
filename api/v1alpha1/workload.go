package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// TODO: Reflect everything related to the workload (VM, Pod, Volume, etc.) being handled.
// Not sure that makes sense in the initial throw.

type Workload struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WorkloadSpec   `json:"spec,omitempty"`
	Status WorkloadStatus `json:"status,omitempty"`
}
