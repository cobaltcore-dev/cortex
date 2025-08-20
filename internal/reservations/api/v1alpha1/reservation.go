// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

// +groupName=cortex.sap
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The kind of reservation.
type ReservationSpecKind string

const (
	// Reservation for a specific virtual machine configuration.
	ReservationSpecKindInstance ReservationSpecKind = "instance"
)

// Specification for an instance reservation.
type ReservationSpecInstance struct {
	// The flavor name of the instance to reserve.
	Flavor string `json:"flavor"`
	// The memory to reserve (e.g., "1Gi", "512Mi").
	Memory resource.Quantity `json:"memory"`
	// The number of vCPUs to reserve (e.g., "2", "500m").
	VCPUs resource.Quantity `json:"vCPUs"`
	// The disk space to reserve (e.g., "10Gi", "500Mi").
	Disk resource.Quantity `json:"disk"`
	// Extra specifications for the instance.
	ExtraSpecs map[string]string `json:"extraSpecs,omitempty"`
}

// ReservationSpec defines the desired state of Reservation.
type ReservationSpec struct {
	Kind ReservationSpecKind `json:"kind"`

	// The project ID to reserve for.
	ProjectID string `json:"projectID"`
	// The domain ID to reserve for.
	DomainID string `json:"domainID"`

	// If reservation kind is instance, this field will contain metadata
	// necessary to determine if the instance reservation can be fulfilled.
	Instance ReservationSpecInstance `json:"instance,omitempty"`
}

// The phase in which the reservation is.
type ReservationStatusPhase string

const (
	// The reservation has been placed and is considered during scheduling.
	ReservationStatusPhaseActive ReservationStatusPhase = "active"
	// The reservation could not be fulfilled.
	ReservationStatusPhaseFailed ReservationStatusPhase = "failed"
)

// The kind of allocation for the reservation.
type ReservationStatusAllocationKind string

const (
	// The kind where a compute node is allocated for the reservation.
	ReservationStatusAllocationKindCompute ReservationStatusAllocationKind = "compute"
)

// Compute allocation for a reservation.
type ReservationStatusAllocationCompute struct {
	// The name of the compute host that was allocated.
	Host string `json:"host"`
}

// Allocation for a reservation.
type ReservationStatusAllocation struct {
	// The kind of allocation.
	Kind ReservationStatusAllocationKind `json:"kind"`

	// If the allocation kind is compute, this field will contain the
	// compute host name of the compute node that was allocated.
	Compute ReservationStatusAllocationCompute `json:"compute,omitempty"`
}

// ReservationStatus defines the observed state of Reservation.
type ReservationStatus struct {
	// The current phase of the reservation.
	Phase ReservationStatusPhase `json:"phase,omitempty"`
	// An error explaining why the reservation is failed, if applicable.
	Error string `json:"error,omitempty"`
	// Allocation for the reservation.
	Allocation ReservationStatusAllocation `json:"allocation,omitempty"`
}

// Reservation is the Schema for the reservations API.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type Reservation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ReservationSpec   `json:"spec,omitempty"`
	Status ReservationStatus `json:"status,omitempty"`
}

// ReservationList contains a list of Reservation.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ReservationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Reservation `json:"items"`
}
