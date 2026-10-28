// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/meta"
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
	//
	// Deprecated: FailoverReservations will be replaced with
	// FailoverHostReservations. See ReservationTypeFailoverHost.
	ReservationTypeFailover ReservationType = "FailoverReservation"

	// ReservationTypeInFlight is a reservation that blocks capacity for virtual
	// machines that are currently being scheduled, to avoid double-booking.
	ReservationTypeInFlight ReservationType = "InFlightReservation"

	// ReservationTypeFailoverHost is a reservation that blocks an entire
	// host for failover purposes, so that another host can gang-evacuate
	// its workloads to a designated spot. If this type is set, the reservation
	// will follow this lifecycle:
	//
	// 1. Designation. The reservation has been created on a host. There may be
	// virtual machines running on this host that need live migration away. As
	// long as not all virtual machines can be migrated away from the host, the
	// reservation stays in this state. For pipeline: no new virtual machine
	// onto this host.
	//
	// 2. Active. No more virtual machines on this host. On evacuation this
	// space can be used.
	//
	// 3. Failed. The descheduling failed repeatedly or takes too long, the host
	// is marked for a certain time to not be designated again.
	//
	// 4. In Use. Ongoing gang evacuation onto this host, which will be reused
	// by subsequent failover events until the evacuation is completed.
	//
	// 5. Revoked (=Deleted). The reservation is no longer valid because it no
	// longer matches any failover partition. This may be because the host has
	// been marked as no longer schedulable or has been otherwise changed.
	//
	// 6. Used (=Deleted). The failover event has been completed. The
	// reservation is no longer valid and the host is transformed back into
	// a regular host.
	ReservationTypeFailoverHost ReservationType = "FailoverHostReservation"
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
	ReservationTypeLabelInFlight          = "in-flight"
	ReservationTypeLabelFailoverHost      = "failover-host"
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
	//
	// Deprecated: replaced by conditions[Ready].ObservedGeneration signaling. Will be removed once old pods are fully rolled out.
	// +kubebuilder:validation:Optional
	ParentGeneration int64 `json:"parentGeneration,omitempty"`

	// Allocations maps workload identifiers to their allocation details.
	// Key: Workload UUID (VM UUID for Nova, Pod UID for Pods, Machine UID for IronCore, etc.)
	// Value: allocation state and metadata
	//
	// +kubebuilder:validation:Optional
	Allocations map[string]CommittedResourceAllocation `json:"allocations,omitempty"`
}

// MatchesGroup reports whether the reservation targets the given project and resource group.
func (s *CommittedResourceReservationSpec) MatchesGroup(projectID, resourceGroup string) bool {
	return s.ProjectID == projectID && s.ResourceGroup == resourceGroup
}

// FailoverReservationSpec defines the spec fields specific to failover reservations.
//
// Deprecated: FailoverReservations will be replaced with FailoverHostReservations.
// See FailoverHostReservationSpec.
type FailoverReservationSpec struct {
	// ResourceGroup is the group/category of the resource (e.g., "hana_medium_v2").
	// +kubebuilder:validation:Optional
	ResourceGroup string `json:"resourceGroup,omitempty"`
}

// InFlightReservationSpec defines the nature and shape of the virtual machine
// that is expected to land on the designated reservation slot. This spec
// carries information needed for the scheduler to produce a valid placement
// for new virtual machines for the duration the virtual machine is still
// in buildup.
type InFlightReservationSpec struct {
	// VMID is the OpenStack server uuid from Nova assigned to the virtual
	// machine expected to land on this reservation slot.
	VMID string `json:"vmID,omitempty"`
	// UserID is the identifier of the user who owns the virtual machine.
	UserID string `json:"userID,omitempty"`
	// ProjectID is the identifier of the project/tenant that owns
	// the virtual machine.
	ProjectID string `json:"projectID,omitempty"`
	// Intent defines which kind of virtual machine lifecycle operation
	// triggered the placement of this in-flight reservation.
	// +kubebuilder:validation:Optional
	Intent SchedulingIntent `json:"intent"`
}

// FailoverHostReservationSpec defines the spec fields specific to failover
// host reservations. See ReservationTypeFailoverHost.
type FailoverHostReservationSpec struct {
	// SourceHosts is a list of hosts inside the same failover partition from
	// which a failover event towards the target host may originate.
	// See Spec.TargetHost for the target host of the failover reservation.
	// +kubebuilder:validation:Optional
	SourceHosts []string `json:"sourceHosts,omitempty"`
}

// ReservationSpec defines the desired state of Reservation.
type ReservationSpec struct {
	// Type of reservation.
	// +kubebuilder:validation:Enum=CommittedResourceReservation;FailoverReservation;InFlightReservation;FailoverHostReservation
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
	//
	// Deprecated: FailoverReservations will be replaced with FailoverHostReservations.
	// +kubebuilder:validation:Optional
	FailoverReservation *FailoverReservationSpec `json:"failoverReservation,omitempty"`

	// InFlightReservation specifies which kind of virtual machine is expected
	// to land on the reserved slot. Set when Type is InFlightReservation.
	InFlightReservation *InFlightReservationSpec `json:"inFlightReservation,omitempty"`

	// FailoverHostReservation contains fields specific to failover host reservations.
	// Only used when Type is FailoverHostReservation.
	// +kubebuilder:validation:Optional
	FailoverHostReservation *FailoverHostReservationSpec `json:"failoverHostReservation,omitempty"`
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
	//
	// Deprecated: replaced by conditions[Ready].ObservedGeneration signaling. Will be removed once old pods are fully rolled out.
	// +kubebuilder:validation:Optional
	ObservedParentGeneration int64 `json:"observedParentGeneration,omitempty"`

	// Allocations maps VM/instance UUIDs to the host they are currently running on.
	// Key: VM/instance UUID, Value: Host name where the VM is currently running.
	// +kubebuilder:validation:Optional
	Allocations map[string]string `json:"allocations,omitempty"`
}

// FailoverReservationStatus defines the status fields specific to failover reservations.
//
// Deprecated: FailoverReservations will be replaced with FailoverHostReservations.
// See FailoverHostReservationStatus.
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

// InFlightReservationStatus defines the status fields specific to
// in-flight reservations.
type InFlightReservationStatus struct{} // No captured state for now.

// FailoverHostReservationStatus defines the status fields specific to failover
// host reservations.
type FailoverHostReservationStatus struct {
	// SourceHost is the actual host from which the failover event originates.
	// Only set when the condition ConditionTypeFailoverHostInUse is True.
	// +kubebuilder:validation:Optional
	SourceHost string `json:"sourceHost,omitempty"`
}

// ReservationConditionType represents the type of condition for a reservation.
type ReservationConditionType = string

const (
	// ConditionTypeFailoverHostDesignated indicates that the reservation has
	// been created on a host. There may be virtual machines running on this
	// host that need live migration away. As long as not all virtual machines
	// can be migrated away from the host, the reservation stays in this state.
	// For pipeline: no new virtual machine onto this host.
	//
	// status=Unknown: It is unknown if the failover host has been designated.
	// status=False: The failover host has not been designated (there was an error).
	// status=True: The failover host has been designated successfully.
	ConditionTypeFailoverHostDesignated ReservationConditionType = "FailoverHostDesignated"

	// ConditionTypeFailoverHostActive indicates that the failover host is
	// active and ready to accept evacuations. No more virtual machines have
	// been seen on this host, so it is empty and ready to receive all vms
	// from another host.
	//
	// status=Unknown: The failover host is not yet ready to be used.
	// status=False: There has been some error or issue preventing the failover
	// host from being active.
	// status=True: The failover host is active and ready to accept evacuations.
	ConditionTypeFailoverHostActive ReservationConditionType = "FailoverHostActive"

	// ConditionTypeFailoverHostInUse indicates that the failover host reservation
	// is currently being used by an ongoing evacuation event with inbound
	// virtual machines being evacuated.
	//
	// status=Unknown: It is unknown if the failover host is currently in use.
	// status=False: The failover host is not currently in use.
	// status=True: The failover host is currently in use.
	ConditionTypeFailoverHostInUse ReservationConditionType = "FailoverHostInUse"
)

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
	//
	// Deprecated: FailoverReservations will be replaced with FailoverHostReservations.
	// +kubebuilder:validation:Optional
	FailoverReservation *FailoverReservationStatus `json:"failoverReservation,omitempty"`

	// InFlightReservation contains status fields specific to in-flight reservations.
	// Only used when Type is InFlightReservation.
	// +kubebuilder:validation:Optional
	InFlightReservation *InFlightReservationStatus `json:"inFlightReservation,omitempty"`

	// FailoverHostReservation contains status fields specific to failover
	// host reservations. Only used when Type is FailoverHostReservation.
	// +kubebuilder:validation:Optional
	FailoverHostReservation *FailoverHostReservationStatus `json:"failoverHostReservation,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".metadata.labels['reservations\\.cortex\\.cloud/type']"
// +kubebuilder:printcolumn:name="Host",type="string",JSONPath=".status.host"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="ResourceGroup",type="string",JSONPath=".spec.committedResourceReservation.resourceGroup",priority=1
// +kubebuilder:printcolumn:name="HA ResourceGroup",type="string",JSONPath=".spec.failoverReservation.resourceGroup",priority=1
// +kubebuilder:printcolumn:name="Project",type="string",JSONPath=".spec.committedResourceReservation.projectID",priority=1
// +kubebuilder:printcolumn:name="AZ",type="string",JSONPath=".spec.availabilityZone",priority=1
// +kubebuilder:printcolumn:name="StartTime",type="string",JSONPath=".spec.startTime",priority=1
// +kubebuilder:printcolumn:name="EndTime",type="string",JSONPath=".spec.endTime",priority=1
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

// IsReady returns true if the reservation has the Ready condition set to True.
func (r *Reservation) IsReady() bool {
	return meta.IsStatusConditionTrue(r.Status.Conditions, ReservationConditionReady)
}

func init() {
	SchemeBuilder.Register(&Reservation{}, &ReservationList{})
}
