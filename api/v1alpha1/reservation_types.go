// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ReservationSpec defines the desired state of Reservation.
type ReservationSpec struct {
	// Domain reflects the logical scheduling domain of this reservation, such as nova, cinder, manila.
	Domain SchedulingDomain `json:"domain"`

	// Resources to be reserved for this reservation request.
	Resources corev1.ResourceList `json:"requests"`

	// StartTime reflects the start timestamp for the reservation.
	StartTime metav1.Time `json:"startTime,omitempty"`

	// EndTime reflects the expiry timestamp for the reservation.
	// TODO: Alternative Duration?
	EndTime metav1.Time `json:"endTime,omitempty"`

	// ActiveTime reflects the time the reservation became used.
	ActiveTime metav1.Time `json:"activeTime,omitempty"`

	// ProjectID ...
	ProjectID string `json:"projectID,omitempty"`

	// Selector ...
	Selector *metav1.LabelSelector `json:"selector,omitempty"`

	// Affinity/Anti-Affinity

	// Nova will contain additional information used by cortex-nova to place the instance.
	// The field may be empty for non-Nova requests
	Nova *ReservationSpecNova `json:"nova,omitempty"`
}

// ReservationSpecNova is an additional specification needed by OpenStack Nova to place the instance.
// TODO: Generalize?
type ReservationSpecNova struct {
	// The project ID to reserve for.
	ProjectID string `json:"projectID,omitempty"`
	// The domain ID to reserve for.
	DomainID string `json:"domainID,omitempty"`
	// The flavor name of the instance to reserve.
	FlavorName string `json:"flavorName,omitempty"`
	// Extra specifications relevant for initial placement of the instance.
	FlavorExtraSpecs map[string]string `json:"flavorExtraSpecs,omitempty"`
}

// ReservationStatusPhase is a high-level summary of the reservation lifecycle.
type ReservationStatusPhase string

const (
	// ReservationStatusPhasePending reflects a not yet scheduled reservation.
	ReservationStatusPhasePending ReservationStatusPhase = "Pending"

	// ReservationStatusPhaseActive indicates that the reservation has been successfully scheduled.
	ReservationStatusPhaseActive ReservationStatusPhase = "Active"

	// ReservationStatusPhaseFailed indicated that the reservation could not be honored.
	ReservationStatusPhaseFailed ReservationStatusPhase = "Failed"

	// ReservationStatusPhaseExpired reflects a reservation past its lifetime ready for garbage collection.
	ReservationStatusPhaseExpired ReservationStatusPhase = "Expired"
)

type ReservationConditionType string

const (
	// ReservationReady reflects the ready status during the handling of the reservation.
	ReservationReady = "Ready"
)

// ReservationStatus defines the observed state of Reservation.
type ReservationStatus struct {
	// The current phase of the reservation.
	Phase ReservationStatusPhase `json:"phase,omitempty"`
	// The current status conditions of the reservation.
	// +kubebuilder:validation:Optional
	Conditions []ReservationCondition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
	// The name of the compute host that was allocated.
	Host string `json:"host"`
}

type ReservationCondition struct {
	// Type of reservation condition.
	Type ReservationConditionType `json:"type"`
	// Status of the condition, one of True, False, Unknown.
	Status metav1.ConditionStatus `json:"status"`
	// Last time we got an update on a given condition.
	// +optional
	LastHeartbeatTime metav1.Time `json:"lastHeartbeatTime,omitempty"`
	// Last time the condition transit from one status to another.
	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
	// (brief) reason for the condition's last transition.
	// +optional
	Reason string `json:"reason,omitempty"`
	// Human-readable message indicating details about last transition.
	// +optional
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Host",type="string",JSONPath=".status.host"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"

// Reservation is the Schema for the reservations API
type Reservation struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of Reservation
	// +required
	Spec ReservationSpec `json:"spec"`

	// status defines the observed state of Reservation
	// +optional
	Status ReservationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ReservationList contains a list of Reservation objects
type ReservationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Reservation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Reservation{}, &ReservationList{})
}
