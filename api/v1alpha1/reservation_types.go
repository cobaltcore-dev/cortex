// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ReservationType defines the type of reservation.
// In Kubernetes, "Type" is the conventional field name for sub-type discrimination
// within a resource (similar to Service.spec.type), while "Kind" refers to the
// resource type itself (Pod, Deployment, etc.).
type ReservationType string

const (
	// ReservationTypeCommittedResource is a reservation for committed/reserved capacity.
	ReservationTypeCommittedResource ReservationType = "CommittedResourceReservation"
	// ReservationTypeFailover is a reservation for failover capacity.
	ReservationTypeFailover ReservationType = "FailoverReservation"
)

// CommittedResourceReservationSpec defines the spec fields specific to committed resource reservations.
type CommittedResourceReservationSpec struct {
	// ResourceName is the name of the resource to reserve (e.g., FlavorName for Nova).
	// +kubebuilder:validation:Optional
	ResourceName string `json:"resourceName,omitempty"`

	// ResourceGroup is the group/category of the resource (e.g., "hana_medium_v2").
	// +kubebuilder:validation:Optional
	ResourceGroup string `json:"resourceGroup,omitempty"`

	// ProjectID is the UUID of the project this reservation belongs to.
	// +kubebuilder:validation:Optional
	ProjectID string `json:"projectID,omitempty"`

	// DomainID is the domain ID to reserve for.
	// +kubebuilder:validation:Optional
	DomainID string `json:"domainID,omitempty"`

	// Creator identifies the system or component that created this reservation.
	// Used to track ownership and for cleanup purposes (e.g., "commitments-syncer").
	// +kubebuilder:validation:Optional
	Creator string `json:"creator,omitempty"`
}

// FailoverReservationSpec defines the spec fields specific to failover reservations.
type FailoverReservationSpec struct {
	// ResourceGroup is the group/category of the resource (e.g., "hana_medium_v2").
	// +kubebuilder:validation:Optional
	ResourceGroup string `json:"resourceGroup,omitempty"`
}

// ReservationSpec defines the desired state of Reservation.
type ReservationSpec struct {
	// Type of reservation.
	// +kubebuilder:validation:Enum=CommittedResourceReservation;FailoverReservation
	// +kubebuilder:validation:Required
	Type ReservationType `json:"type"`

	// SchedulingDomain specifies the scheduling domain for this reservation (e.g., "nova", "ironcore").
	// +kubebuilder:validation:Optional
	SchedulingDomain string `json:"schedulingDomain,omitempty"`

	// Resources to reserve for this instance.
	// +kubebuilder:validation:Optional
	Resources map[string]resource.Quantity `json:"resources,omitempty"`

	// StartTime is the time when the reservation becomes active.
	// +kubebuilder:validation:Optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// EndTime is the time when the reservation expires.
	// +kubebuilder:validation:Optional
	EndTime *metav1.Time `json:"endTime,omitempty"`

	// TargetHost is the desired compute host where the reservation should be placed.
	// This is a generic name that represents different concepts depending on the scheduling domain:
	// - For Nova: the hypervisor hostname
	// - For Pods: the node name
	// The scheduler will attempt to place the reservation on this host.
	// +kubebuilder:validation:Optional
	TargetHost string `json:"targetHost,omitempty"`

	// CommittedResourceReservation contains fields specific to committed resource reservations.
	// Only used when Type is CommittedResourceReservation.
	// +kubebuilder:validation:Optional
	CommittedResourceReservation *CommittedResourceReservationSpec `json:"committedResourceReservation,omitempty"`

	// FailoverReservation contains fields specific to failover reservations.
	// Only used when Type is FailoverReservation.
	// +kubebuilder:validation:Optional
	FailoverReservation *FailoverReservationSpec `json:"failoverReservation,omitempty"`
}

const (
	// ReservationConditionReady indicates whether the reservation is active and ready.
	ReservationConditionReady = "Ready"
)

// CommittedResourceReservationStatus defines the status fields specific to committed resource reservations.
type CommittedResourceReservationStatus struct {
	// Allocations lists the VM/instance UUIDs that are currently allocated against this reservation.
	// +kubebuilder:validation:Optional
	Allocations []string `json:"allocations,omitempty"`
}

// FailoverReservationStatus defines the status fields specific to failover reservations.
type FailoverReservationStatus struct {
	// Allocations maps VM/instance UUIDs to the host they are currently allocated on.
	// Key: VM/instance UUID, Value: Host name where the VM is currently running.
	// +kubebuilder:validation:Optional
	Allocations map[string]string `json:"allocations,omitempty"`
}

// ReservationStatus defines the observed state of Reservation.
type ReservationStatus struct {
	// The current status conditions of the reservation.
	// Conditions include:
	// - type: Ready
	//   status: True|False|Unknown
	//   reason: ReservationReady
	//   message: Reservation is successfully scheduled
	//   lastTransitionTime: <timestamp>
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// Host is the actual host where the reservation is placed.
	// This is set by the scheduler after successful placement and reflects the current state.
	// It should match Spec.TargetHost when the reservation is successfully placed.
	// This is a generic name that represents different concepts depending on the scheduling domain:
	// - For Nova: the hypervisor hostname
	// - For Pods: the node name
	// +kubebuilder:validation:Optional
	Host string `json:"host,omitempty"`

	// CommittedResourceReservation contains status fields specific to committed resource reservations.
	// Only used when Type is CommittedResourceReservation.
	// +kubebuilder:validation:Optional
	CommittedResourceReservation *CommittedResourceReservationStatus `json:"committedResourceReservation,omitempty"`

	// FailoverReservation contains status fields specific to failover reservations.
	// Only used when Type is FailoverReservation.
	// +kubebuilder:validation:Optional
	FailoverReservation *FailoverReservationStatus `json:"failoverReservation,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Host",type="string",JSONPath=".status.host"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"

// Reservation is the Schema for the reservations API
type Reservation struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Reservation
	// +required
	Spec ReservationSpec `json:"spec"`

	// status defines the observed state of Reservation
	// +optional
	Status ReservationStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ReservationList contains a list of Reservation
type ReservationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Reservation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Reservation{}, &ReservationList{})
}
