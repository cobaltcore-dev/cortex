// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProjectQuotaSpec defines the desired state of ProjectQuota.
// Populated from PUT /v1/projects/:uuid/quota payloads (liquid.ServiceQuotaRequest).
// Each ProjectQuota CRD represents quota for ONE project in ONE availability zone.
// See: https://pkg.go.dev/github.com/sapcc/go-api-declarations/liquid#ServiceQuotaRequest
type ProjectQuotaSpec struct {
	// ProjectID of the OpenStack project this quota belongs to.
	// Corresponds to the :uuid in the PUT URL path.
	// +kubebuilder:validation:Required
	ProjectID string `json:"projectID"`

	// ProjectName is the human-readable name of the OpenStack project.
	// Extracted from liquid.ServiceQuotaRequest.ProjectMetadata.Name.
	// +kubebuilder:validation:Optional
	ProjectName string `json:"projectName,omitempty"`

	// DomainID of the OpenStack domain this project belongs to.
	// Extracted from liquid.ServiceQuotaRequest.ProjectMetadata.Domain.UUID.
	// +kubebuilder:validation:Required
	DomainID string `json:"domainID"`

	// DomainName is the human-readable name of the OpenStack domain.
	// Extracted from liquid.ServiceQuotaRequest.ProjectMetadata.Domain.Name.
	// +kubebuilder:validation:Optional
	DomainName string `json:"domainName,omitempty"`

	// AvailabilityZone is the AZ this quota CRD covers (e.g. "qa-de-1a").
	// In a multi-cluster setup, this determines which cluster the CRD is routed to.
	// +kubebuilder:validation:Required
	AvailabilityZone string `json:"availabilityZone"`

	// Quota maps LIQUID resource names to their quota value for THIS availability zone.
	// Key: liquid.ResourceName (e.g. "hw_version_hana_v2_ram")
	// Value: per-AZ quota from liquid.AZResourceQuotaRequest.Quota
	// See: https://pkg.go.dev/github.com/sapcc/go-api-declarations/liquid#AZResourceQuotaRequest
	// +kubebuilder:validation:Optional
	Quota map[string]int64 `json:"quota,omitempty"`
}

// ProjectQuotaStatus defines the observed state of ProjectQuota.
// Usage values correspond to liquid.AZResourceUsageReport fields reported via /report-usage.
// See: https://pkg.go.dev/github.com/sapcc/go-api-declarations/liquid#AZResourceUsageReport
type ProjectQuotaStatus struct {
	// ObservedGeneration is the most recent spec generation that the controller has processed.
	// Used to distinguish spec changes (which require TotalUsage recompute) from
	// CommittedResource changes (which only need PaygUsage recompute).
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// TotalUsage tracks per-resource total resource consumption in this AZ (all VMs in this project+AZ).
	// Persisted by the quota controller; updated by full reconcile and HV instance diffs.
	// Key: liquid.ResourceName
	// +kubebuilder:validation:Optional
	TotalUsage map[string]int64 `json:"totalUsage,omitempty"`

	// PaygUsage tracks per-resource pay-as-you-go usage in this AZ.
	// Derived as TotalUsage - CRUsage (clamped >= 0).
	// Key: liquid.ResourceName
	// +kubebuilder:validation:Optional
	PaygUsage map[string]int64 `json:"paygUsage,omitempty"`

	// LastReconcileAt is when the controller last reconciled this project's quota (any path).
	// +kubebuilder:validation:Optional
	LastReconcileAt *metav1.Time `json:"lastReconcileAt,omitempty"`

	// LastFullReconcileAt is when the periodic full reconcile last completed for this project.
	// Used as the watermark for isVMNewSinceLastReconcile (incremental add detection).
	// Only updated by ReconcilePeriodic, NOT by PaygUsage recomputes or incremental deltas.
	// +kubebuilder:validation:Optional
	LastFullReconcileAt *metav1.Time `json:"lastFullReconcileAt,omitempty"`

	// Conditions holds the current status conditions.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Project",type="string",JSONPath=".spec.projectID"
// +kubebuilder:printcolumn:name="AZ",type="string",JSONPath=".spec.availabilityZone"
// +kubebuilder:printcolumn:name="Domain",type="string",JSONPath=".spec.domainID"
// +kubebuilder:printcolumn:name="LastReconcile",type="date",JSONPath=".status.lastReconcileAt"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"

// ProjectQuota is the Schema for the projectquotas API.
// It persists quota values pushed by Limes via the LIQUID quota endpoint
// (PUT /v1/projects/:uuid/quota → liquid.ServiceQuotaRequest).
// Each CRD stores quota for one project in one availability zone.
// In a multi-cluster setup, it is routed to the cluster serving that AZ.
// See: https://pkg.go.dev/github.com/sapcc/go-api-declarations/liquid#ServiceQuotaRequest
type ProjectQuota struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// +required
	Spec ProjectQuotaSpec `json:"spec"`

	// +optional
	Status ProjectQuotaStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ProjectQuotaList contains a list of ProjectQuota
type ProjectQuotaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProjectQuota `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ProjectQuota{}, &ProjectQuotaList{})
}
