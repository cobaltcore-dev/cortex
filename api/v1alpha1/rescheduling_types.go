// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Label keys for Rescheduling metadata.
// Labels follow Kubernetes naming conventions using reverse-DNS notation.
const (
	// LabelFailoverHostDrain marks the Rescheduling objects created by the
	// failover host drain controller, so it can list its own drains and enforce
	// the fleet-wide single-drain slot. Set to "true" on those objects.
	LabelFailoverHostDrain = "reservations.cortex.cloud/failover-host-drain"
)

// ReschedulingAction defines the action a rescheduling request asks the
// Rescheduler to perform. The action names an action-specific sub-spec that
// carries its target and constraints.
type ReschedulingAction string

const (
	// ReschedulingActionDrain requests that the entities on the targeted hosts
	// be moved away, optionally reserving the freed capacity.
	ReschedulingActionDrain ReschedulingAction = "drain"
)

// DrainEntityKind defines the kind of entity targeted by a drain action.
type DrainEntityKind string

const (
	// DrainEntityKindVM targets a virtual machine. The entity moved is the VM
	// itself: its Nova server is live-migrated to another host. The ref name is
	// the Nova server uuid, which is also the name of the (cluster-scoped)
	// vm-operator VirtualMachine resource. During the Nova->CRD migration a
	// VirtualMachine resource may not exist yet for a given uuid; such a ref is
	// still valid and simply does not resolve to a CRD until one is created
	// (this transitional case is expected to go away).
	DrainEntityKindVM DrainEntityKind = "vm"
	// DrainEntityKindReservation targets a reservation. The entity moved is the
	// reservation object itself (a movable failover or committed-resource
	// Reservation), not any workload that may be associated with it. The ref
	// name is the Reservation resource name.
	DrainEntityKindReservation DrainEntityKind = "reservation"
)

// HostRef references a host to act on: a (cluster-scoped) Hypervisor resource
// identified by name. It is a purpose-built reference rather than a generic
// corev1.ObjectReference, whose own documentation discourages new embedded uses
// (ignored fields, no per-use validation) and recommends a locally focused type.
//
// Name is the host id and is carried in the ref so routine handling does not
// need to fetch the Hypervisor. AvailabilityZone is the routing key the
// multicluster client uses to reach the right cluster; it defaults to the
// request's spec.availabilityZone (a drain request is AZ-scoped). Hypervisor
// resources currently live in a compute cluster and are not AZ-unique once an
// AZ spans several compute clusters; resolving a host to its cluster is left to
// the multicluster client so the ref survives that topology change (and a
// possible future move of the Hypervisor CRD into the lighthouse cluster).
type HostRef struct {
	// Name is the name of the Hypervisor (the host name).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// AvailabilityZone is the availability zone the host belongs to. Defaults to
	// the request's availability zone when unset.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=253
	AvailabilityZone string `json:"availabilityZone,omitempty"`
}

// EntityRef references a movable entity: a vm-operator VirtualMachine or a
// Reservation, discriminated by Kind. Like HostRef it is a purpose-built
// reference (see HostRef) carrying the entity id in Name so routine handling
// does not need to fetch the referenced resource.
//
// Both VirtualMachine and Reservation are cluster-scoped (so no namespace) and
// live in the lighthouse cluster routed by AvailabilityZone, which defaults to
// the request's spec.availabilityZone.
type EntityRef struct {
	// Kind is the kind of the referenced entity.
	// +kubebuilder:validation:Enum=vm;reservation
	// +kubebuilder:validation:Required
	Kind DrainEntityKind `json:"kind"`

	// Name is the name of the referenced entity: the Nova server uuid for a vm
	// (the VirtualMachine resource name) or the Reservation resource name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// AvailabilityZone is the availability zone the entity belongs to. Defaults
	// to the request's availability zone when unset.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=253
	AvailabilityZone string `json:"availabilityZone,omitempty"`
}

// DrainConstraintMode defines how strictly the drain constraints are enforced.
type DrainConstraintMode string

const (
	// DrainConstraintModeAll requires all targeted entities to be moved for the
	// request to succeed.
	DrainConstraintModeAll DrainConstraintMode = "all"
	// DrainConstraintModeBestEffort moves as many targeted entities as possible
	// without failing the request when some cannot be moved.
	DrainConstraintModeBestEffort DrainConstraintMode = "bestEffort"
)

// DrainTargetEntity identifies a single entity that should be moved off a host.
type DrainTargetEntity struct {
	// Entity references the entity to move.
	// +kubebuilder:validation:Required
	Entity EntityRef `json:"entity"`
}

// DrainTargetHost names a host and the entities on it that should be moved.
type DrainTargetHost struct {
	// Host references the host to drain entities from.
	// +kubebuilder:validation:Required
	Host HostRef `json:"host"`

	// Entities are the entities on the host that should be moved. If empty, all
	// movable entities on the host are drained.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=1000
	Entities []DrainTargetEntity `json:"entities,omitempty"`
}

// DrainTarget describes where the drain action should act.
type DrainTarget struct {
	// Hosts are the hosts to drain entities from.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=10
	Hosts []DrainTargetHost `json:"hosts,omitempty"`
}

// DrainConstraints configures how the drain action is executed.
type DrainConstraints struct {
	// Mode defines how strictly the constraints are enforced.
	// +kubebuilder:validation:Enum=all;bestEffort
	// +kubebuilder:validation:Optional
	Mode DrainConstraintMode `json:"mode,omitempty"`
}

// DrainActionSpec defines the spec fields specific to the drain action.
type DrainActionSpec struct {
	// Reserve indicates whether the capacity freed by draining should be
	// reserved to prevent it from being used by other workloads.
	// +kubebuilder:validation:Optional
	Reserve bool `json:"reserve,omitempty"`

	// Target describes the hosts and entities to drain.
	// +kubebuilder:validation:Optional
	Target DrainTarget `json:"target,omitempty,omitzero"`

	// Constraints configures how the drain action is executed.
	// +kubebuilder:validation:Optional
	Constraints DrainConstraints `json:"constraints,omitempty,omitzero"`
}

// ReschedulingSpec defines the desired state of Rescheduling.
// The spec is immutable once created: a request is a one-shot instruction, the
// Rescheduler decides how to execute it. The drain action requires its sub-spec.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable once created"
// +kubebuilder:validation:XValidation:rule="self.action != 'drain' || has(self.drain)",message="drain action requires a drain spec"
type ReschedulingSpec struct {
	// Action names the rescheduling action to perform.
	// +kubebuilder:validation:Enum=drain
	// +kubebuilder:validation:Required
	Action ReschedulingAction `json:"action"`

	// AvailabilityZone specifies the availability zone this request belongs to.
	// It is used to route the request to the lighthouse cluster of that AZ.
	// +kubebuilder:validation:Optional
	AvailabilityZone string `json:"availabilityZone,omitempty"`

	// Drain contains fields specific to the drain action.
	// Only used when Action is drain.
	// +kubebuilder:validation:Optional
	Drain *DrainActionSpec `json:"drain,omitempty"`
}

// ReschedulingState reflects the lifecycle stage of a rescheduling
// request as observed by the Rescheduler.
type ReschedulingState string

const (
	// ReschedulingStatePending indicates the request has been submitted
	// but not yet picked up by the Rescheduler.
	ReschedulingStatePending ReschedulingState = "Pending"
	// ReschedulingStateAcknowledged indicates the Rescheduler has
	// received the request.
	ReschedulingStateAcknowledged ReschedulingState = "Acknowledged"
	// ReschedulingStateProcessing indicates the request is being worked on.
	ReschedulingStateProcessing ReschedulingState = "Processing"
	// ReschedulingStateAccepted indicates the request was accepted for
	// execution.
	ReschedulingStateAccepted ReschedulingState = "Accepted"
	// ReschedulingStateRejected indicates the request was rejected and
	// will not be executed.
	ReschedulingStateRejected ReschedulingState = "Rejected"
	// ReschedulingStateSucceeded indicates the request completed
	// successfully.
	ReschedulingStateSucceeded ReschedulingState = "Succeeded"
	// ReschedulingStateFailed indicates the request failed to complete.
	ReschedulingStateFailed ReschedulingState = "Failed"
)

// ReschedulingMove records a single entity relocation decided by the scheduling
// pass: an entity is moved off its source host onto the chosen target host.
type ReschedulingMove struct {
	// Entity references the moved entity.
	// +kubebuilder:validation:Required
	Entity EntityRef `json:"entity"`

	// SourceHost is the host the entity is moved away from.
	// +kubebuilder:validation:Required
	SourceHost HostRef `json:"sourceHost"`

	// TargetHost is the host the entity is moved onto.
	// +kubebuilder:validation:Required
	TargetHost HostRef `json:"targetHost"`
}

// ReschedulingStatus defines the observed state of Rescheduling.
type ReschedulingStatus struct {
	// State is the current lifecycle stage of the request.
	// The transition rule enforces the state machine at the apiserver for all
	// writers; self-transitions (unchanged state) are allowed so conditions-only
	// status writes pass, and terminal states have no outgoing transitions.
	// +kubebuilder:validation:Enum=Pending;Acknowledged;Processing;Accepted;Rejected;Succeeded;Failed
	// +kubebuilder:default=Pending
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf || (oldSelf == 'Pending' && (self == 'Acknowledged' || self == 'Rejected')) || (oldSelf == 'Acknowledged' && (self == 'Processing' || self == 'Rejected')) || (oldSelf == 'Processing' && (self == 'Accepted' || self == 'Rejected' || self == 'Failed')) || (oldSelf == 'Accepted' && (self == 'Succeeded' || self == 'Failed'))",message="illegal rescheduling request state transition"
	State ReschedulingState `json:"state,omitempty"`

	// Moves are the entity relocations the scheduling pass committed to when the
	// request was accepted. Empty until the request reaches Accepted.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=1000
	Moves []ReschedulingMove `json:"moves,omitempty"`

	// BlockedHosts are the hosts this request currently blocks from receiving
	// incoming placement (the host-block taken for a drain with reserve). It is
	// set while the request is active and cleared on rollback or, for a succeeded
	// request, once BlockExpiresAt has passed. The drain-block scheduling filter
	// reads this field across all requests to exclude these hosts.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=10
	BlockedHosts []string `json:"blockedHosts,omitempty"`

	// BlockExpiresAt is the time after which the host-block of a succeeded request
	// may be released. It is set when an accepted request succeeds and lets the
	// block linger for a grace period so incoming placement does not immediately
	// land on a just-drained host.
	// +kubebuilder:validation:Optional
	BlockExpiresAt *metav1.Time `json:"blockExpiresAt,omitempty"`

	// Conditions carries generic, human-readable detail about the rescheduling
	// request (e.g. messages explaining the current situation). It is not the
	// authoritative lifecycle representation: status.state is the single source
	// of truth for the request's lifecycle and drives reconcile control flow.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Action",type="string",JSONPath=".spec.action"
// +kubebuilder:printcolumn:name="AZ",type="string",JSONPath=".spec.availabilityZone"
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state"
// +kubebuilder:printcolumn:name="Created",type="date",JSONPath=".metadata.creationTimestamp"

// Rescheduling is the Schema for the reschedulings API
type Rescheduling struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Rescheduling
	// +required
	Spec ReschedulingSpec `json:"spec"`

	// status defines the observed state of Rescheduling
	// +optional
	Status ReschedulingStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ReschedulingList contains a list of Rescheduling
type ReschedulingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Rescheduling `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Rescheduling{}, &ReschedulingList{})
}
