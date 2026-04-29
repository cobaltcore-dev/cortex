// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
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

// Label keys for Reservation metadata.
// Labels follow Kubernetes naming conventions using reverse-DNS notation
const (
	// ===== Common Reservation Labels =====

	// LabelReservationType identifies the type of reservation.
	// This label is present on all reservations to enable type-based filtering.
	LabelReservationType = "reservations.cortex.cloud/type"

	// Reservation type label values
	ReservationTypeLabelCommittedResource = "committed-resource"
	ReservationTypeLabelFailover          = "failover"
)

// Annotation keys for Reservation metadata.
const (
	// AnnotationCreatorRequestID tracks the request ID that created this reservation.
	// Used for end-to-end traceability across API calls, controller reconciles, and scheduler invocations.
	AnnotationCreatorRequestID = "reservations.cortex.cloud/creator-request-id"
)

// CommittedResourceAllocation represents a workload's assignment to a committed resource reservation slot.
// The workload could be a VM (Nova/IronCore), Pod (Kubernetes), or other resource.
type CommittedResourceAllocation struct {
	// Timestamp when this workload was assigned to the reservation.
	// +kubebuilder:validation:Required
	CreationTimestamp metav1.Time `json:"creationTimestamp"`

	// Resources consumed by this instance.
	// +kubebuilder:validation:Required
	Resources map[hv1.ResourceName]resource.Quantity `json:"resources"`
}

// CommittedResourceReservationSpec defines the spec fields specific to committed resource reservations.
type CommittedResourceReservationSpec struct {
	// ResourceName is the name of the resource to reserve. (e.g. flavor name for Nova)
	// +kubebuilder:validation:Optional
	ResourceName string `json:"resourceName,omitempty"`

	// CommitmentUUID is the UUID of the commitment that this reservation corresponds to.
	// +kubebuilder:validation:Optional
	CommitmentUUID string `json:"commitmentUUID,omitempty"`

	// ResourceGroup is the group/category of the resource (e.g., flavor group for Nova)
	// +kubebuilder:validation:Optional
	ResourceGroup string `json:"resourceGroup,omitempty"`

	// +kubebuilder:validation:Optional
	ProjectID string `json:"projectID,omitempty"`

	// +kubebuilder:validation:Optional
	DomainID string `json:"domainID,omitempty"`

	// Creator identifies the system or component that created this reservation.
	// Used to track ownership and for cleanup purposes (e.g., "commitments-syncer").
	// +kubebuilder:validation:Optional
	Creator string `json:"creator,omitempty"`

	// ParentGeneration is the Generation of the CommittedResource CRD at the time this
	// reservation was last written by the CommittedResource controller. The Reservation
	// controller echoes it to Status.CommittedResourceReservation.ObservedParentGeneration
	// once it has processed the reservation, allowing the CR controller to wait until
	// all child reservations are up-to-date before accepting.
	// Zero means the field is not set (syncer-created reservations, no parent CR).
	// +kubebuilder:validation:Optional
	ParentGeneration int64 `json:"parentGeneration,omitempty"`

	// Allocations maps workload identifiers to their allocation details.
	// Key: Workload UUID (VM UUID for Nova, Pod UID for Pods, Machine UID for IronCore, etc.)
	// Value: allocation state and metadata
	// +kubebuilder:validation:Optional
	Allocations map[string]CommittedResourceAllocation `json:"allocations,omitempty"`
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
	SchedulingDomain SchedulingDomain `json:"schedulingDomain,omitempty"`

	// AvailabilityZone specifies the availability zone for this reservation, if restricted to a specific AZ.
	// +kubebuilder:validation:Optional
	AvailabilityZone string `json:"availabilityZone,omitempty"`

	// Resources to reserve for this instance.
	// +kubebuilder:validation:Optional
	Resources map[hv1.ResourceName]resource.Quantity `json:"resources,omitempty"`

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
	// ObservedParentGeneration is the Spec.CommittedResourceReservation.ParentGeneration value
	// that this Reservation controller last processed. When it matches ParentGeneration in spec,
	// the CR controller knows this reservation is up-to-date for the current CR spec version.
	// +kubebuilder:validation:Optional
	ObservedParentGeneration int64 `json:"observedParentGeneration,omitempty"`

	// Allocations maps VM/instance UUIDs to the host they are currently running on.
	// Key: VM/instance UUID, Value: Host name where the VM is currently running.
	// +kubebuilder:validation:Optional
	Allocations map[string]string `json:"allocations,omitempty"`
}

// FailoverReservationStatus defines the status fields specific to failover reservations.
type FailoverReservationStatus struct {
	// Allocations maps VM/instance UUIDs to the host they are currently allocated on.
	// Key: VM/instance UUID, Value: Host name where the VM is currently running.
	// +kubebuilder:validation:Optional
	Allocations map[string]string `json:"allocations,omitempty"`

	// LastChanged tracks when the reservation was last modified.
	// This is used to track pending changes that need acknowledgment.
	// +kubebuilder:validation:Optional
	LastChanged *metav1.Time `json:"lastChanged,omitempty"`

	// AcknowledgedAt is the timestamp when the last change was acknowledged.
	// When nil, the reservation is in a pending state awaiting acknowledgment.
	// This does not affect the Ready condition - reservations are still considered
	// ready even when not yet acknowledged.
	// +kubebuilder:validation:Optional
	AcknowledgedAt *metav1.Time `json:"acknowledgedAt,omitempty"`
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
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".metadata.labels['reservations\\.cortex\\.cloud/type']"
// +kubebuilder:printcolumn:name="Host",type="string",JSONPath=".status.host"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="ResourceGroup",type="string",JSONPath=".spec.committedResourceReservation.resourceGroup"
// +kubebuilder:printcolumn:name="Project",type="string",JSONPath=".spec.committedResourceReservation.projectID"
// +kubebuilder:printcolumn:name="AZ",type="string",JSONPath=".spec.availabilityZone"
// +kubebuilder:printcolumn:name="StartTime",type="string",JSONPath=".spec.startTime",priority=1
// +kubebuilder:printcolumn:name="EndTime",type="string",JSONPath=".spec.endTime"
// +kubebuilder:printcolumn:name="Resources",type="string",JSONPath=".spec.resources",priority=1
// +kubebuilder:printcolumn:name="LastChanged",type="date",JSONPath=".status.failoverReservation.lastChanged",priority=1
// +kubebuilder:printcolumn:name="AcknowledgedAt",type="date",JSONPath=".status.failoverReservation.acknowledgedAt",priority=1
// +kubebuilder:printcolumn:name="CR Allocations",type="string",JSONPath=".status.committedResourceReservation.allocations",priority=1
// +kubebuilder:printcolumn:name="HA Allocations",type="string",JSONPath=".status.failoverReservation.allocations",priority=1

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
