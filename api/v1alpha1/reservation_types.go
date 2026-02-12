// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ReservationType defines the type of reservation.
type ReservationType string

const (
	// ReservationTypeCommittedResource is a reservation for committed/reserved capacity.
	ReservationTypeCommittedResource ReservationType = "CommittedResourceReservation"
	// ReservationTypeFailover is a reservation for failover capacity.
	ReservationTypeFailover ReservationType = "FailoverReservation"
)

// ReservationSelector defines the selector criteria for a reservation.
type ReservationSelector struct {
	// IsNUMAAlignedHost specifies whether the host should be NUMA-aligned.
	// +kubebuilder:validation:Optional
	IsNUMAAlignedHost bool `json:"isNUMAAlignedHost,omitempty"`
}

// ReservationSpec defines the desired state of Reservation.
type ReservationSpec struct {
	// A remark that can be used to identify the creator of the reservation.
	// This can be used to clean up reservations synced from external systems
	// without touching reservations created manually or by other systems.
	Creator string `json:"creator,omitempty"`

	// Resources to reserve for this instance.
	Resources map[string]resource.Quantity `json:"resources,omitempty"`

	// Selector specifies criteria for selecting appropriate hosts.
	// +kubebuilder:validation:Optional
	Selector ReservationSelector `json:"selector,omitempty"`

	// SchedulingDomain specifies the scheduling domain for this reservation (e.g., "nova", "ironcore").
	// +kubebuilder:validation:Optional
	SchedulingDomain string `json:"schedulingDomain,omitempty"`

	// Type of reservation. Defaults to CommittedResourceReservation if not specified.
	// +kubebuilder:validation:Enum=CommittedResourceReservation;FailoverReservation
	// +kubebuilder:default=CommittedResourceReservation
	Type ReservationType `json:"type,omitempty"`

	// --- Time-related fields ---

	// StartTime is the time when the reservation becomes active.
	// +kubebuilder:validation:Optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// EndTime is the time when the reservation expires.
	// +kubebuilder:validation:Optional
	EndTime *metav1.Time `json:"endTime,omitempty"`

	// NOTE: ActiveTime was removed. Use StartTime and EndTime to define the active period.
	// If you need a duration-based activation, calculate EndTime = StartTime + Duration externally.

	// --- Scheduling-related fields (e.g., for Nova) ---

	// ProjectID is the UUID of the project this reservation belongs to.
	// +kubebuilder:validation:Optional
	ProjectID string `json:"projectID,omitempty"`

	// DomainID is the domain ID to reserve for.
	// +kubebuilder:validation:Optional
	DomainID string `json:"domainID,omitempty"`

	// ResourceName is the name of the resource to reserve (e.g., FlavorName for Nova).
	// +kubebuilder:validation:Optional
	ResourceName string `json:"resourceName,omitempty"`

	// ResourceExtraSpecs contains extra specifications relevant for initial placement
	// of the instance (e.g., FlavorExtraSpecs for Nova).
	// +kubebuilder:validation:Optional
	ResourceExtraSpecs map[string]string `json:"resourceExtraSpecs,omitempty"`

	// --- Placement fields (desired state) ---

	// Host is the desired compute host where the reservation should be placed.
	// This is a generic name that represents different concepts depending on the scheduling domain:
	// - For Nova: the hypervisor hostname
	// - For Pods: the node name
	// The scheduler will attempt to place the reservation on this host.
	// +kubebuilder:validation:Optional
	Host string `json:"host,omitempty"`

	// ConnectTo the instances that lossy use this reservation
	// The key is the instance, e.g., VM UUID, the value is usecase specific and can be empty
	// E.g., for failover reservations, this maps VM/instance UUIDs to the host the VM is currently running on.
	// +kubebuilder:validation:Optional
	ConnectTo map[string]string `json:"connectTo,omitempty"`
}

// The phase in which the reservation is.
type ReservationStatusPhase string

const (
	// The reservation has been placed and is considered during scheduling.
	ReservationStatusPhaseActive ReservationStatusPhase = "active"
	// The reservation could not be fulfilled.
	ReservationStatusPhaseFailed ReservationStatusPhase = "failed"
)

const (
	// Something went wrong during the handling of the reservation.
	ReservationConditionError = "Error"
)

// ReservationStatus defines the observed state of Reservation.
type ReservationStatus struct {
	// The current phase of the reservation.
	Phase ReservationStatusPhase `json:"phase,omitempty"`

	// The current status conditions of the reservation.
	// Conditions include:
	// - type: Ready
	//   status: True|False|Unknown
	//   reason: ReservationReady
	//   message: Reservation is successfully scheduled
	//   lastTransitionTime: <timestamp>
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// ObservedHost is the actual host where the reservation is on
	// This is set by the scheduler after successful placement and reflects the current state.
	// It should match Spec.Host when the reservation is successfully placed.
	// This is a generic name that represents different concepts depending on the scheduling domain:
	// - For Nova: the hypervisor hostname
	// - For Pods: the node name
	// +kubebuilder:validation:Optional
	ObservedHost string `json:"observedHost,omitempty"`

	// ObservedConnectTo maps VM/instance UUIDs to the host they are currently allocated on.
	// This tracks which VMs are actually using this failover reservation and their current placement.
	// Key: VM/instance UUID, Value: Host name where the VM is currently running.
	// Only used when Type is FailoverReservation.
	// This should reflect the actual state and may differ from Spec.ConnectTo during transitions.
	// +kubebuilder:validation:Optional
	ObservedConnectTo map[string]string `json:"observedConnectTo,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Host",type="string",JSONPath=".status.observedHost"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"

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
