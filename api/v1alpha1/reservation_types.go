// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Additional specifications needed to place the reservation.
type ReservationSchedulerSpec struct {
	// If the type of scheduler is cortex-nova, this field will contain additional
	// information used by cortex-nova to place the instance.
	CortexNova *ReservationSchedulerSpecCortexNova `json:"cortexNova,omitempty"`
}

// Additional specifications needed by cortex-nova to place the instance.
type ReservationSchedulerSpecCortexNova struct {
	// The project ID to reserve for.
	ProjectID string `json:"projectID,omitempty"`
	// The domain ID to reserve for.
	DomainID string `json:"domainID,omitempty"`
	// The flavor name of the instance to reserve.
	FlavorName string `json:"flavorName,omitempty"`
	// Extra specifications relevant for initial placement of the instance.
	FlavorExtraSpecs map[string]string `json:"flavorExtraSpecs,omitempty"`
}

// ReservationSpec defines the desired state of Reservation.
type ReservationSpec struct {
	// A remark that can be used to identify the creator of the reservation.
	// This can be used to clean up reservations synced from external systems
	// without touching reservations created manually or by other systems.
	Creator string `json:"creator,omitempty"`
	// Specification of the scheduler that will handle the reservation.
	Scheduler ReservationSchedulerSpec `json:"scheduler,omitempty"`
	// Resources requested to reserve for this instance.
	Requests map[string]resource.Quantity `json:"requests,omitempty"`
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
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
	// The name of the compute host that was allocated.
	Host string `json:"host"`
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

func (*Reservation) URI() string     { return "reservations.cortex.cloud/v1alpha1" }
func (*ReservationList) URI() string { return "reservations.cortex.cloud/v1alpha1" }

func init() {
	SchemeBuilder.Register(&Reservation{}, &ReservationList{})
}
