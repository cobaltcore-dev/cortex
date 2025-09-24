// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Additional specifications needed to place the reservation.
type ComputeReservationSchedulerSpec struct {
	// If the type of scheduler is cortex-nova, this field will contain additional
	// information used by cortex-nova to place the instance.
	CortexNova *ComputeReservationSchedulerSpecCortexNova `json:"cortexNova,omitempty"`
}

// Additional specifications needed by cortex-nova to place the instance.
type ComputeReservationSchedulerSpecCortexNova struct {
	// The project ID to reserve for.
	ProjectID string `json:"projectID,omitempty"`
	// The domain ID to reserve for.
	DomainID string `json:"domainID,omitempty"`
	// The flavor name of the instance to reserve.
	FlavorName string `json:"flavorName,omitempty"`
	// Extra specifications relevant for initial placement of the instance.
	FlavorExtraSpecs map[string]string `json:"flavorExtraSpecs,omitempty"`
}

// ComputeReservationSpec defines the desired state of ComputeReservation.
type ComputeReservationSpec struct {
	// A remark that can be used to identify the creator of the reservation.
	// This can be used to clean up reservations synced from external systems
	// without touching reservations created manually or by other systems.
	Creator string `json:"creator,omitempty"`
	// Specification of the scheduler that will handle the reservation.
	Scheduler ComputeReservationSchedulerSpec `json:"scheduler,omitempty"`
	// Resources requested to reserve for this instance.
	Requests map[string]resource.Quantity `json:"requests,omitempty"`
}

// The phase in which the reservation is.
type ComputeReservationStatusPhase string

const (
	// The reservation has been placed and is considered during scheduling.
	ComputeReservationStatusPhaseActive ComputeReservationStatusPhase = "active"
	// The reservation could not be fulfilled.
	ComputeReservationStatusPhaseFailed ComputeReservationStatusPhase = "failed"
)

// ComputeReservationStatus defines the observed state of ComputeReservation.
type ComputeReservationStatus struct {
	// The current phase of the reservation.
	Phase ComputeReservationStatusPhase `json:"phase,omitempty"`
	// An error explaining why the reservation is failed, if applicable.
	Error string `json:"error,omitempty"`
	// The name of the compute host that was allocated.
	Host string `json:"host"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=cres
// +kubebuilder:printcolumn:name="Host",type="string",JSONPath=".status.host"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Error",type="string",JSONPath=".status.error"

// ComputeReservation is the Schema for the computereservations API
type ComputeReservation struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of ComputeReservation
	// +required
	Spec ComputeReservationSpec `json:"spec"`

	// status defines the observed state of ComputeReservation
	// +optional
	Status ComputeReservationStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ComputeReservationList contains a list of ComputeReservation
type ComputeReservationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ComputeReservation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ComputeReservation{}, &ComputeReservationList{})
}
