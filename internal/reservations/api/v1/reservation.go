// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ReservationSpec defines the desired state of Reservation.
type ReservationSpec struct {
	Commitment Commitment `json:"commitment,omitempty"`
}

// ReservationStatus defines the observed state of Reservation.
type ReservationStatus struct {
	Reserved bool `json:"reserved,omitempty"`
	Host     Host `json:"host,omitempty"`
}

// Reservation is the Schema for the reservations API.
type Reservation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ReservationSpec   `json:"spec,omitempty"`
	Status ReservationStatus `json:"status,omitempty"`
}

// Conform to the runtime.Object interface.
func (in *Reservation) DeepCopyObject() runtime.Object {
	return &Reservation{
		TypeMeta:   in.TypeMeta,
		ObjectMeta: in.ObjectMeta,
		Spec:       in.Spec,
		Status:     in.Status,
	}
}

// ReservationList contains a list of Reservation.
type ReservationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Reservation `json:"items"`
}

// Conform to the runtime.Object interface.
func (in *ReservationList) DeepCopyObject() runtime.Object {
	return &ReservationList{
		TypeMeta: in.TypeMeta,
		ListMeta: in.ListMeta,
		Items:    in.Items,
	}
}
