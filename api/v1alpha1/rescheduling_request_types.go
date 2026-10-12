// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	// DrainEntityKindVM targets a virtual machine.
	DrainEntityKindVM DrainEntityKind = "vm"
	// DrainEntityKindReservation targets a reservation.
	DrainEntityKindReservation DrainEntityKind = "reservation"
)

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
	// Kind is the kind of the targeted entity.
	// +kubebuilder:validation:Enum=vm;reservation
	// +kubebuilder:validation:Required
	Kind DrainEntityKind `json:"kind"`

	// Ref references the entity to move (e.g. the Nova server uuid for a vm).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=253
	Ref string `json:"ref"`
}

// DrainTargetHost names a host and the entities on it that should be moved.
type DrainTargetHost struct {
	// Host is the name of the host to drain entities from.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=253
	Host string `json:"host"`

	// Entities are the entities on the host that should be moved.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=100
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

// ReschedulingRequestSpec defines the desired state of ReschedulingRequest.
// The spec is immutable once created: a request is a one-shot instruction, the
// Rescheduler decides how to execute it. The drain action requires its sub-spec.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable once created"
// +kubebuilder:validation:XValidation:rule="self.action != 'drain' || has(self.drain)",message="drain action requires a drain spec"
type ReschedulingRequestSpec struct {
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

// ReschedulingRequestState reflects the lifecycle stage of a rescheduling
// request as observed by the Rescheduler.
type ReschedulingRequestState string

const (
	// ReschedulingRequestStatePending indicates the request has been submitted
	// but not yet picked up by the Rescheduler.
	ReschedulingRequestStatePending ReschedulingRequestState = "Pending"
	// ReschedulingRequestStateAcknowledged indicates the Rescheduler has
	// received the request.
	ReschedulingRequestStateAcknowledged ReschedulingRequestState = "Acknowledged"
	// ReschedulingRequestStateProcessing indicates the request is being worked on.
	ReschedulingRequestStateProcessing ReschedulingRequestState = "Processing"
	// ReschedulingRequestStateAccepted indicates the request was accepted for
	// execution.
	ReschedulingRequestStateAccepted ReschedulingRequestState = "Accepted"
	// ReschedulingRequestStateRejected indicates the request was rejected and
	// will not be executed.
	ReschedulingRequestStateRejected ReschedulingRequestState = "Rejected"
	// ReschedulingRequestStateSucceeded indicates the request completed
	// successfully.
	ReschedulingRequestStateSucceeded ReschedulingRequestState = "Succeeded"
	// ReschedulingRequestStateFailed indicates the request failed to complete.
	ReschedulingRequestStateFailed ReschedulingRequestState = "Failed"
)

const (
	// ReschedulingRequestConditionReady indicates the request has been
	// successfully processed.
	ReschedulingRequestConditionReady = "Ready"
	// ReschedulingRequestConditionInProgress indicates the request is currently
	// being processed.
	ReschedulingRequestConditionInProgress = "InProgress"
)

// ReschedulingRequestStatus defines the observed state of ReschedulingRequest.
type ReschedulingRequestStatus struct {
	// State is the current lifecycle stage of the request.
	// The transition rule enforces the state machine at the apiserver for all
	// writers; self-transitions (unchanged state) are allowed so conditions-only
	// status writes pass, and terminal states have no outgoing transitions.
	// +kubebuilder:validation:Enum=Pending;Acknowledged;Processing;Accepted;Rejected;Succeeded;Failed
	// +kubebuilder:default=Pending
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf || (oldSelf == 'Pending' && (self == 'Acknowledged' || self == 'Rejected')) || (oldSelf == 'Acknowledged' && (self == 'Processing' || self == 'Rejected')) || (oldSelf == 'Processing' && (self == 'Accepted' || self == 'Rejected' || self == 'Failed')) || (oldSelf == 'Accepted' && (self == 'Succeeded' || self == 'Failed'))",message="illegal rescheduling request state transition"
	State ReschedulingRequestState `json:"state,omitempty"`

	// The current status conditions of the rescheduling request.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Action",type="string",JSONPath=".spec.action"
// +kubebuilder:printcolumn:name="AZ",type="string",JSONPath=".spec.availabilityZone"
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Created",type="date",JSONPath=".metadata.creationTimestamp"

// ReschedulingRequest is the Schema for the reschedulingrequests API
type ReschedulingRequest struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of ReschedulingRequest
	// +required
	Spec ReschedulingRequestSpec `json:"spec"`

	// status defines the observed state of ReschedulingRequest
	// +optional
	Status ReschedulingRequestStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ReschedulingRequestList contains a list of ReschedulingRequest
type ReschedulingRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ReschedulingRequest `json:"items"`
}

// IsReady returns true if the request has the Ready condition set to True.
func (r *ReschedulingRequest) IsReady() bool {
	return meta.IsStatusConditionTrue(r.Status.Conditions, ReschedulingRequestConditionReady)
}

func init() {
	SchemeBuilder.Register(&ReschedulingRequest{}, &ReschedulingRequestList{})
}
