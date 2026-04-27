// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

// +kubebuilder:object:generate=true
// +kubebuilder:skip
// +groupName=compute.ironcore.dev
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupVersion is group version used to register these objects.
var GroupVersion = schema.GroupVersion{Group: "compute.ironcore.dev", Version: "v1alpha1"}

// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
var SchemeBuilder = &schemeBuilder{GroupVersion: GroupVersion}

// AddToScheme adds the types in this group-version to the given scheme.
var AddToScheme = SchemeBuilder.AddToScheme

// schemeBuilder wraps runtime.SchemeBuilder to provide a Register method
// compatible with the controller-runtime scheme.Builder API.
type schemeBuilder struct {
	GroupVersion schema.GroupVersion
	runtime.SchemeBuilder
}

// Register adds one or more objects to the SchemeBuilder so they can be added to a Scheme.
func (b *schemeBuilder) Register(object ...runtime.Object) {
	b.SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(b.GroupVersion, object...)
		metav1.AddToGroupVersion(s, b.GroupVersion)
		return nil
	})
}

// Build returns a new Scheme containing all registered types.
func (b *schemeBuilder) Build() (*runtime.Scheme, error) {
	s := runtime.NewScheme()
	return s, b.AddToScheme(s)
}
