// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Only confirmed and guaranteed commitments result in active Reservation slots.
type CommitmentStatus string

const (
	// CommitmentStatusPlanned: StartTime not yet reached; no resources guaranteed, no Reservation slots yet.
	CommitmentStatusPlanned CommitmentStatus = "planned"
	// CommitmentStatusPending: StartTime reached; no resources guaranteed, no Reservation slots yet.
	CommitmentStatusPending CommitmentStatus = "pending"
	// CommitmentStatusGuaranteed: StartTime not reached yet; resources are guaranteed latest starting from StartTime, Reservation slots in sync.
	CommitmentStatusGuaranteed CommitmentStatus = "guaranteed"
	// CommitmentStatusConfirmed: StartTime reached; resources are guaranteed, Reservation slots in sync.
	CommitmentStatusConfirmed CommitmentStatus = "confirmed"
	// CommitmentStatusSuperseded: replaced by another commitment; no resources guaranteed, Reservation slots removed.
	CommitmentStatusSuperseded CommitmentStatus = "superseded"
	// CommitmentStatusExpired: past EndTime; no resources guaranteed, Reservation slots removed.
	CommitmentStatusExpired CommitmentStatus = "expired"
)

// CommittedResourceType identifies the kind of resource a commitment covers.
type CommittedResourceType string

const (
	// CommittedResourceTypeMemory: RAM commitment; drives flavor-based Reservation slot creation.
	CommittedResourceTypeMemory CommittedResourceType = "memory"
	// CommittedResourceTypeCores: CPU core commitment; verified arithmetically, no Reservation slots created.
	CommittedResourceTypeCores CommittedResourceType = "cores"
)

// CommittedResourceSpec defines the desired state of CommittedResource,
type CommittedResourceSpec struct {
	// UUID of the commitment this resource corresponds to.
	// +kubebuilder:validation:Required
	CommitmentUUID string `json:"commitmentUUID"`

	// SchedulingDomain specifies the scheduling domain for this committed resource (e.g., "nova", "ironcore").
	// +kubebuilder:validation:Optional
	SchedulingDomain SchedulingDomain `json:"schedulingDomain,omitempty"`

	// FlavorGroupName identifies the flavor group this commitment targets, e.g. "kvm_v2_hana_s".
	// +kubebuilder:validation:Required
	FlavorGroupName string `json:"flavorGroupName"`

	// ResourceType identifies the kind of resource committed: memory drives Reservation slots; cores uses an arithmetic check only.
	// +kubebuilder:validation:Enum=memory;cores
	// +kubebuilder:validation:Required
	ResourceType CommittedResourceType `json:"resourceType"`

	// Amount is the total committed quantity.
	// memory: MiB expressed in K8s binary SI notation (e.g. "1280Gi", "640Mi").
	// cores: integer core count (e.g. "40").
	// +kubebuilder:validation:Required
	Amount resource.Quantity `json:"amount"`

	// AvailabilityZone specifies the availability zone for this commitment.
	// +kubebuilder:validation:Required
	AvailabilityZone string `json:"availabilityZone"`

	// ProjectID of the OpenStack project this commitment belongs to.
	// +kubebuilder:validation:Required
	ProjectID string `json:"projectID"`

	// DomainID of the OpenStack domain this commitment belongs to.
	// +kubebuilder:validation:Required
	DomainID string `json:"domainID"`

	// StartTime is the activation time for Reservation slots.
	// Nil for guaranteed commitments (slots are active from creation); set to ConfirmedAt for confirmed ones.
	// +kubebuilder:validation:Optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// EndTime is when Reservation slots expire. Nil for unbounded commitments with no expiry.
	// +kubebuilder:validation:Optional
	EndTime *metav1.Time `json:"endTime,omitempty"`

	// ConfirmedAt is when the commitment was confirmed.
	// +kubebuilder:validation:Optional
	ConfirmedAt *metav1.Time `json:"confirmedAt,omitempty"`

	// State is the lifecycle state of the commitment.
	// +kubebuilder:validation:Enum=planned;pending;guaranteed;confirmed;superseded;expired
	// +kubebuilder:validation:Required
	State CommitmentStatus `json:"state"`

	// AllowRejection controls what the CommittedResource controller does when placement fails
	// for a guaranteed or confirmed commitment.
	// true  — controller may reject: on failure, child Reservations are rolled back and the CR
	//         is marked Rejected. Use this when the caller is making a first-time placement
	//         decision and a "no" answer is acceptable (e.g. the change-commitments API).
	// false — controller must retry: on failure, existing child Reservations are kept and the
	//         CR is set to Reserving so the controller retries later. Use this when the caller
	//         is restoring already-committed state that Cortex must honour (e.g. the syncer).
	// Only meaningful for state=guaranteed or state=confirmed; ignored for all other states.
	// +kubebuilder:validation:Optional
	AllowRejection bool `json:"allowRejection,omitempty"`
}

// CommittedResourceStatus defines the observed state of CommittedResource.
type CommittedResourceStatus struct {
	// AcceptedSpec is a snapshot of Spec from the last successful reconcile.
	// Used by rollbackToAccepted to restore the exact previously-accepted placement (AZ, amount,
	// project, domain, flavor group) even when the current spec has already been mutated to a new value.
	// +kubebuilder:validation:Optional
	AcceptedSpec *CommittedResourceSpec `json:"acceptedSpec,omitempty"`

	// AcceptedAt is when the controller last successfully reconciled the spec into Reservation slots.
	// +kubebuilder:validation:Optional
	AcceptedAt *metav1.Time `json:"acceptedAt,omitempty"`

	// LastChanged is when the spec was last written by the syncer.
	// When AcceptedAt is older than LastChanged, the controller has pending work.
	// +kubebuilder:validation:Optional
	LastChanged *metav1.Time `json:"lastChanged,omitempty"`

	// LastReconcileAt is when the controller last ran its reconcile loop for this resource.
	// +kubebuilder:validation:Optional
	LastReconcileAt *metav1.Time `json:"lastReconcileAt,omitempty"`

	// AssignedInstances holds the UUIDs of VM instances deterministically assigned to this committed resource.
	// Populated by the usage reconciler; used to compute UsedResources and drive the quota controller.
	// +kubebuilder:validation:Optional
	AssignedInstances []string `json:"assignedInstances,omitempty"`

	// UsedResources is the total resource consumption of assigned VM instances, keyed by resource type
	// (e.g. "memory" in MiB binary SI, "cpu" as core count). Populated by the usage reconciler.
	// +kubebuilder:validation:Optional
	UsedResources map[string]resource.Quantity `json:"usedResources,omitempty"`

	// LastUsageReconcileAt is when the usage reconciler last updated AssignedInstances and UsedResources.
	// +kubebuilder:validation:Optional
	LastUsageReconcileAt *metav1.Time `json:"lastUsageReconcileAt,omitempty"`

	// UsageObservedGeneration is the CR generation that the usage reconciler last processed.
	// Follows the Kubernetes observedGeneration pattern: when this differs from
	// metadata.generation the cooldown is bypassed so spec changes (e.g. shrink) are reflected
	// immediately rather than waiting for the next cooldown interval.
	// +kubebuilder:validation:Optional
	UsageObservedGeneration *int64 `json:"usageObservedGeneration,omitempty"`

	// Conditions holds the current status conditions.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

const (
	// CommittedResourceConditionReady indicates whether the CommittedResource has been
	// successfully reconciled into active Reservation CRDs.
	CommittedResourceConditionReady = "Ready"

	// Condition reasons set by the CommittedResource controller.
	CommittedResourceReasonAccepted  = "Accepted"
	CommittedResourceReasonPlanned   = "Planned"
	CommittedResourceReasonReserving = "Reserving"
	CommittedResourceReasonRejected  = "Rejected"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Project",type="string",JSONPath=".spec.projectID"
// +kubebuilder:printcolumn:name="FlavorGroup",type="string",JSONPath=".spec.flavorGroupName"
// +kubebuilder:printcolumn:name="ResourceType",type="string",JSONPath=".spec.resourceType"
// +kubebuilder:printcolumn:name="AZ",type="string",JSONPath=".spec.availabilityZone"
// +kubebuilder:printcolumn:name="Amount",type="string",JSONPath=".spec.amount"
// +kubebuilder:printcolumn:name="UsedMemory",type="string",JSONPath=".status.usedResources.memory",priority=1
// +kubebuilder:printcolumn:name="UsedCPU",type="string",JSONPath=".status.usedResources.cpu",priority=1
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".spec.state"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="StartTime",type="date",JSONPath=".spec.startTime",priority=1
// +kubebuilder:printcolumn:name="EndTime",type="date",JSONPath=".spec.endTime",priority=1

// CommittedResource is the Schema for the committedresources API
type CommittedResource struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// +required
	Spec CommittedResourceSpec `json:"spec"`

	// +optional
	Status CommittedResourceStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// CommittedResourceList contains a list of CommittedResource
type CommittedResourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CommittedResource `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CommittedResource{}, &CommittedResourceList{})
}
