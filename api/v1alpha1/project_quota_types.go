// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ResourceQuota holds the quota for a single resource with per-AZ breakdown.
// Maps to liquid.ResourceQuotaRequest from the LIQUID API.
// See: https://pkg.go.dev/github.com/sapcc/go-api-declarations/liquid#ResourceQuotaRequest
type ResourceQuota struct {
	// Quota is the total quota across all AZs (for compatibility).
	// Corresponds to liquid.ResourceQuotaRequest.Quota.
	// +kubebuilder:validation:Required
	Quota int64 `json:"quota"`

	// PerAZ holds the per-availability-zone quota breakdown.
	// Key: availability zone name, Value: quota for that AZ.
	// Only populated for AZSeparatedTopology resources.
	// Corresponds to liquid.ResourceQuotaRequest.PerAZ[az].Quota.
	// See: https://pkg.go.dev/github.com/sapcc/go-api-declarations/liquid#AZResourceQuotaRequest
	// +kubebuilder:validation:Optional
	PerAZ map[string]int64 `json:"perAZ,omitempty"`
}

// ResourceQuotaUsage holds per-AZ PAYG usage for a single resource.
type ResourceQuotaUsage struct {
	// PerAZ holds per-availability-zone PAYG usage values.
	// Key: availability zone name, Value: PAYG usage in that AZ.
	// +kubebuilder:validation:Optional
	PerAZ map[string]int64 `json:"perAZ,omitempty"`
}

// ProjectQuotaSpec defines the desired state of ProjectQuota.
// Populated from PUT /v1/projects/:uuid/quota payloads (liquid.ServiceQuotaRequest).
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

	// Quota maps LIQUID resource names to their per-AZ quota.
	// Key: liquid.ResourceName (e.g. "hw_version_hana_v2_ram")
	// Mirrors liquid.ServiceQuotaRequest.Resources with AZSeparatedTopology.
	// See: https://pkg.go.dev/github.com/sapcc/go-api-declarations/liquid#ServiceQuotaRequest
	// +kubebuilder:validation:Optional
	Quota map[string]ResourceQuota `json:"quota,omitempty"`
}

// ProjectQuotaStatus defines the observed state of ProjectQuota.
// Usage values correspond to liquid.AZResourceUsageReport fields reported via /report-usage.
// See: https://pkg.go.dev/github.com/sapcc/go-api-declarations/liquid#AZResourceUsageReport
type ProjectQuotaStatus struct {
	// TotalUsage tracks per-resource per-AZ total resource consumption (all VMs in this project).
	// Persisted by the quota controller; updated by full reconcile and HV instance diffs.
	// Key: liquid.ResourceName
	// +kubebuilder:validation:Optional
	TotalUsage map[string]ResourceQuotaUsage `json:"totalUsage,omitempty"`

	// PaygUsage tracks per-resource per-AZ pay-as-you-go usage.
	// Derived as TotalUsage - CRUsage (clamped >= 0).
	// Key: liquid.ResourceName
	// +kubebuilder:validation:Optional
	PaygUsage map[string]ResourceQuotaUsage `json:"paygUsage,omitempty"`

	// LastReconcileAt is when the controller last reconciled this project's quota.
	// +kubebuilder:validation:Optional
	LastReconcileAt *metav1.Time `json:"lastReconcileAt,omitempty"`

	// Conditions holds the current status conditions.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Project",type="string",JSONPath=".spec.projectID"
// +kubebuilder:printcolumn:name="Domain",type="string",JSONPath=".spec.domainID"
// +kubebuilder:printcolumn:name="LastReconcile",type="date",JSONPath=".status.lastReconcileAt"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"

// ProjectQuota is the Schema for the projectquotas API.
// It persists quota values pushed by Limes via the LIQUID quota endpoint
// (PUT /v1/projects/:uuid/quota → liquid.ServiceQuotaRequest).
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
