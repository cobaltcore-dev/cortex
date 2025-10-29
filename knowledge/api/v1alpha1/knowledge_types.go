// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// Dependencies required for extracting the knowledge.
type KnowledgeDependenciesSpec struct {
	// Datasources required for extracting this knowledge.
	// If provided, all datasources must have the same database secret reference
	// so the knowledge can be joined across multiple database tables.
	// +kubebuilder:validation:Optional
	Datasources []corev1.ObjectReference `json:"datasources,omitempty"`

	// Other knowledges this knowledge depends on.
	// +kubebuilder:validation:Optional
	Knowledges []corev1.ObjectReference `json:"knowledges,omitempty"`
}

type KnowledgeExtractorSpec struct {
	// The name of the extractor.
	Name string `json:"name,omitempty"`

	// Additional configuration for the extractor.
	// +kubebuilder:validation:Optional
	Config runtime.RawExtension `json:"config"`
}

type KnowledgeSpec struct {
	// The operator by which this knowledge should be extracted.
	Operator string `json:"operator,omitempty"`

	// The feature extractor to use for extracting this knowledge.
	Extractor KnowledgeExtractorSpec `json:"extractor,omitempty"`

	// The desired recency of this knowledge, i.e. how old it can be until
	// it needs to be re-extracted.
	// +kubebuilder:default="60s"
	Recency metav1.Duration `json:"recency"`

	// A human-readable description of the knowledge to be extracted.
	// +kubebuilder:validation:Optional
	Description string `json:"description,omitempty"`

	// Dependencies required for extracting this knowledge.
	// +kubebuilder:validation:Optional
	Dependencies KnowledgeDependenciesSpec `json:"dependencies"`

	// Database credentials for the database where the knowledge will be stored.
	//
	// Note: this is a legacy feature to stay compatible with the cortex scheduler.
	// Once the scheduler is moved to use the knowledge via CRs only, we can
	// remove this.
	//
	// The secret should contain the following keys:
	// - "username": The database username.
	// - "password": The database password.
	// - "host": The database host.
	// - "port": The database port.
	// - "database": The database name.
	DatabaseSecretRef *corev1.SecretReference `json:"databaseSecretRef"`

	// Whether the knowledge should only be stored in the database and not
	// in the CR status.
	//
	// Note: this is a legacy feature. Features should always contain condensed
	// knowledge in the CR status for easy access.
	// +kubebuilder:default=false
	StoreInDatabaseOnly bool `json:"storeInDatabaseOnly,omitempty"`
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
	// Something went wrong during the extraction of the knowledge.
	KnowledgeConditionError = "Error"
)

type KnowledgeStatus struct {
	// When the knowledge was last successfully extracted.
	// +kubebuilder:validation:Optional
	LastExtracted metav1.Time `json:"lastExtracted"`
	// The time it took to perform the last extraction.
	// +kubebuilder:validation:Optional
	Took metav1.Duration `json:"took"`

	// The raw data behind the extracted knowledge, e.g. a list of features.
	// +kubebuilder:validation:Optional
	Raw runtime.RawExtension `json:"raw"`
	// The number of features extracted, or 1 if the knowledge is not a list.
	// +kubebuilder:validation:Optional
	RawLength int `json:"rawLength,omitempty"`

	// The current status conditions of the knowledge.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Operator",type="string",JSONPath=".spec.operator"
// +kubebuilder:printcolumn:name="Created",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Extracted",type="date",JSONPath=".status.lastExtracted"
// +kubebuilder:printcolumn:name="Took",type="string",JSONPath=".status.took"
// +kubebuilder:printcolumn:name="Recency",type="string",JSONPath=".spec.recency"
// +kubebuilder:printcolumn:name="Features",type="integer",JSONPath=".status.rawLength"

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
