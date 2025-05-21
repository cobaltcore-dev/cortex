// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// FeatureSpec defines the desired state of Feature.
type FeatureSpec struct {
	Foo string `json:"foo,omitempty"`
}

// FeatureStatus defines the observed state of Feature.
type FeatureStatus struct {
}

// Feature is the Schema for the features API.
type Feature struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FeatureSpec   `json:"spec,omitempty"`
	Status FeatureStatus `json:"status,omitempty"`
}

// Conform to the runtime.Object interface.
func (in *Feature) DeepCopyObject() runtime.Object {
	return &Feature{
		TypeMeta:   in.TypeMeta,
		ObjectMeta: in.ObjectMeta,
		Spec:       in.Spec,
		Status:     in.Status,
	}
}

// FeatureList contains a list of Feature.
type FeatureList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Feature `json:"items"`
}

// Conform to the runtime.Object interface.
func (in *FeatureList) DeepCopyObject() runtime.Object {
	return &FeatureList{
		TypeMeta: in.TypeMeta,
		ListMeta: in.ListMeta,
		Items:    in.Items,
	}
}
