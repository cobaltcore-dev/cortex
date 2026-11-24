// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"encoding/json"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

type KnowledgeExtractorSpec struct {
	// The name of the go extractor registered in cortex.
	Name string `json:"name"`

	// Additional configuration for the extractor.
	// +kubebuilder:validation:Optional
	Config runtime.RawExtension `json:"config"`
}

type KnowledgeExpressionSpec struct {
	// Expression (https://expr-lang.org) evaluated over the inputs, which are
	// available as variables named by their var. Fields of input objects are
	// accessed by their go field names. The expression must return a list.
	Run string `json:"run"`

	// Name of the registered go type of a single output feature. The output
	// of the expression is strictly decoded into a list of this type, so
	// consumers can unbox the knowledge with the same type.
	Output string `json:"output"`
}

// +kubebuilder:validation:XValidation:rule="has(self.extractor) != has(self.expression)",message="exactly one of extractor or expression must be set"
type KnowledgeSpec struct {
	// SchedulingDomain defines in which scheduling domain this knowledge
	// is used (e.g., nova, cinder, manila).
	SchedulingDomain SchedulingDomain `json:"schedulingDomain"`

	// The go feature extractor to use for extracting this knowledge.
	// +kubebuilder:validation:Optional
	Extractor *KnowledgeExtractorSpec `json:"extractor,omitempty"`

	// The expression to use for extracting this knowledge.
	// +kubebuilder:validation:Optional
	Expression *KnowledgeExpressionSpec `json:"expression,omitempty"`

	// Inputs passed to the extractor or expression. Changes to the referenced
	// datasources and knowledges trigger a re-extraction of this knowledge.
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=var
	Inputs []InputSpec `json:"inputs,omitempty"`

	// The desired recency of this knowledge, i.e. how old it can be until
	// it needs to be re-extracted.
	// +kubebuilder:default="60s"
	Recency metav1.Duration `json:"recency"`

	// A human-readable description of the knowledge to be extracted.
	// +kubebuilder:validation:Optional
	Description string `json:"description,omitempty"`
}

// Convert raw features to a list of strongly typed feature structs.
func UnboxFeatureList[T any](raw runtime.RawExtension) ([]T, error) {
	var t []T
	if len(raw.Raw) == 0 {
		return t, nil
	}
	var rawSerialized struct {
		Features []T `json:"features"`
	}
	if err := json.Unmarshal(raw.Raw, &rawSerialized); err != nil {
		return t, err
	}
	return rawSerialized.Features, nil
}

// Convert a list of strongly typed feature structs to raw features.
func BoxFeatureList[T any](features []T) (runtime.RawExtension, error) {
	raw := runtime.RawExtension{}
	var err error
	rawSerialized := struct {
		Features []T `json:"features"`
	}{
		Features: features,
	}
	raw.Raw, err = json.Marshal(rawSerialized)
	return raw, err
}

const (
	// The knowledge is ready to be used.
	KnowledgeConditionReady = "Ready"
)

type KnowledgeStatus struct {
	// When the knowledge was last successfully extracted.
	// +kubebuilder:validation:Optional
	LastExtracted metav1.Time `json:"lastExtracted"`

	// When the extracted knowledge content last changed.
	// Updated only when the Raw data actually changes, not on every reconcile.
	// +kubebuilder:validation:Optional
	LastContentChange metav1.Time `json:"lastContentChange,omitempty"`

	// The raw data behind the extracted knowledge, e.g. a list of features.
	// +kubebuilder:validation:Optional
	Raw runtime.RawExtension `json:"raw"`
	// The number of features extracted, or 1 if the knowledge is not a list.
	// +kubebuilder:validation:Optional
	RawLength int `json:"rawLength,omitempty"`

	// The current status conditions of the knowledge.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Domain",type="string",JSONPath=".spec.schedulingDomain"
// +kubebuilder:printcolumn:name="Created",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Extracted",type="date",JSONPath=".status.lastExtracted"
// +kubebuilder:printcolumn:name="Changed",type="date",JSONPath=".status.lastContentChange"
// +kubebuilder:printcolumn:name="Recency",type="string",JSONPath=".spec.recency"
// +kubebuilder:printcolumn:name="Features",type="integer",JSONPath=".status.rawLength"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"

// Knowledge is the Schema for the knowledges API
type Knowledge struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Knowledge
	// +required
	Spec KnowledgeSpec `json:"spec"`

	// status defines the observed state of Knowledge
	// +optional
	Status KnowledgeStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// KnowledgeList contains a list of Knowledge
type KnowledgeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Knowledge `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Knowledge{}, &KnowledgeList{})
}
