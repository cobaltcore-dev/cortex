// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The kind of reservation.
type ComputeReservationSpecKind string

const (
	// Reservation for a specific virtual machine configuration.
	ComputeReservationSpecKindInstance ComputeReservationSpecKind = "instance"
	// Reservation for a bare resource.
	ComputeReservationSpecKindBareResource ComputeReservationSpecKind = "bare"
)

// Specification for an instance reservation.
type ComputeReservationSpecInstance struct {
	// The flavor name of the instance to reserve.
	Flavor string `json:"flavor"`
	// Resources requested to reserve for this instance.
	Requests map[string]resource.Quantity `json:"requests,omitempty"`
	// Extra specifications for the instance.
	ExtraSpecs map[string]string `json:"extraSpecs,omitempty"`
}

// Specification for a bare resource reservation
type ComputeReservationSpecBareResource struct {
	// Resources requested to reserve.
	Requests map[string]resource.Quantity `json:"requests,omitempty"`
}

// ComputeReservationSpec defines the desired state of ComputeReservation.
type ComputeReservationSpec struct {
	Kind ComputeReservationSpecKind `json:"kind"`

	// The project ID to reserve for.
	ProjectID string `json:"projectID"`
	// The domain ID to reserve for.
	DomainID string `json:"domainID"`

	// If reservation kind is instance, this field will contain metadata
	// necessary to determine if the instance reservation can be fulfilled.
	Instance ComputeReservationSpecInstance `json:"instance,omitempty"`
	// If reservation kind is bare resource, this field will contain metadata
	// necessary to determine if the bare resource reservation can be fulfilled.
	BareResource ComputeReservationSpecBareResource `json:"bareResource,omitempty"`
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
