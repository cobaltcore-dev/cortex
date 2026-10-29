// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ChangeRequestPhase represents the FSM phase of a CommittedResourceChangeRequest.
type ChangeRequestPhase string

const (
	// ChangeRequestPhasePreparing: change request created; sanity checks and optional snapshot in progress.
	ChangeRequestPhasePreparing ChangeRequestPhase = "Preparing"
	// ChangeRequestPhaseAllocating: CR specs are being written.
	ChangeRequestPhaseAllocating ChangeRequestPhase = "Allocating"
	// ChangeRequestPhaseAwaitingScheduling: CR specs written; waiting for placement success.
	ChangeRequestPhaseAwaitingScheduling ChangeRequestPhase = "AwaitingScheduling"
	// ChangeRequestPhaseRollingBack: a CR was rejected or timed out; restoring pre-change state.
	ChangeRequestPhaseRollingBack ChangeRequestPhase = "RollingBack"
	// ChangeRequestPhaseDryRunCleanup: dry-run would-accept; restoring pre-change state.
	ChangeRequestPhaseDryRunCleanup ChangeRequestPhase = "DryRunCleanup"
	// ChangeRequestPhaseAccepted: terminal; change request committed successfully.
	ChangeRequestPhaseAccepted ChangeRequestPhase = "Accepted"
	// ChangeRequestPhaseRejected: terminal; change request rolled back.
	ChangeRequestPhaseRejected ChangeRequestPhase = "Rejected"
	// ChangeRequestPhaseSuperseded: terminal; abandoned because a newer change request for the same AZ took over.
	ChangeRequestPhaseSuperseded ChangeRequestPhase = "Superseded"
)

// TxSource identifies the originator of a CommittedResourceChangeRequest.
type TxSource string

const (
	// TxSourceAPI: change request created by the change-commitments HTTP API.
	TxSourceAPI TxSource = "api"
	// TxSourceSyncer: change request created by the Limes syncer.
	TxSourceSyncer TxSource = "syncer"
)

const (
	// ChangeRequestConditionProgressing is True while the change request is advancing normally.
	// False + reason indicates a problem while still non-terminal.
	ChangeRequestConditionProgressing = "Progressing"

	// ChangeRequestConditionFinished is True once the change request has reached a terminal phase.
	ChangeRequestConditionFinished = "Finished"

	// Reasons for ChangeRequestConditionProgressing=False (non-terminal problems).
	ChangeRequestReasonStalled        = "Stalled"
	ChangeRequestReasonTimedOut       = "TimedOut"
	ChangeRequestReasonError          = "Error"
	ChangeRequestReasonRetryExhausted = "RetryExhausted"

	// Reasons for ChangeRequestConditionFinished=True.
	ChangeRequestReasonAccepted   = "Accepted"
	ChangeRequestReasonRejected   = "Rejected"
	ChangeRequestReasonSuperseded = "Superseded"
)

// CommittedResourceChangeRequestSpec defines the desired change for a CommittedResourceChangeRequest.
type CommittedResourceChangeRequestSpec struct {
	// CorrelationID is the request/correlation ID.
	// +kubebuilder:validation:Optional
	CorrelationID string `json:"correlationID,omitempty"`

	// AvailabilityZone is the availability zone this change request is scoped to.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	AvailabilityZone string `json:"availabilityZone"`

	// Source identifies who created this change request.
	// +kubebuilder:validation:Enum=api;syncer
	// +kubebuilder:validation:Required
	Source TxSource `json:"source"`

	// AllowRejection controls whether the change request may be rejected and rolled back
	// on placement failure. When false, the reconciler retries until Accepted or Superseded.
	// +kubebuilder:validation:Optional
	AllowRejection bool `json:"allowRejection,omitempty"`

	// DryRun controls whether the change request commits or only probes schedulability.
	// +kubebuilder:validation:Optional
	DryRun bool `json:"dryRun,omitempty"`

	// NewState maps commitmentUUID to the desired CommittedResourceSpec.
	// A nil value means the corresponding CommittedResource should be deleted.
	// +kubebuilder:validation:Optional
	NewState map[string]*CommittedResourceSpec `json:"newState,omitempty"`
}

// CommittedResourceChangeRequestStatus defines the observed state of a CommittedResourceChangeRequest.
type CommittedResourceChangeRequestStatus struct {
	// Phase is the current FSM phase of the change request.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="oldSelf == '' || !['Accepted','Rejected','Superseded'].exists(t, t == oldSelf) || self == oldSelf",message="phase cannot transition out of a terminal state"
	Phase ChangeRequestPhase `json:"phase,omitempty"`

	// SnapshotBefore records the CommittedResource spec state before this change request.
	// Key is commitmentUUID; nil value means the CR did not exist before (will be deleted on rollback).
	// Populated in Preparing when AllowRejection=true.
	// +kubebuilder:validation:Optional
	SnapshotBefore map[string]*CommittedResourceSpec `json:"snapshotBefore,omitempty"`

	// UpsertedCommitments holds references to CommittedResource CRDs written by this change request.
	// Watched in AwaitingScheduling to gate on placement success.
	// These are plain object references, not owner references.
	// +kubebuilder:validation:Optional
	UpsertedCommitments []corev1.ObjectReference `json:"upsertedCommitments,omitempty"`

	// Conditions holds Progressing and Finished conditions.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// Message is a human-readable description of the current state or terminal outcome.
	// +kubebuilder:validation:Optional
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=crcr
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="AZ",type="string",JSONPath=".spec.availabilityZone"
// +kubebuilder:printcolumn:name="Source",type="string",JSONPath=".spec.source"
// +kubebuilder:printcolumn:name="DryRun",type="boolean",JSONPath=".spec.dryRun",priority=1
// +kubebuilder:printcolumn:name="Progressing",type="string",JSONPath=".status.conditions[?(@.type=='Progressing')].status",priority=1
// +kubebuilder:printcolumn:name="Finished",type="string",JSONPath=".status.conditions[?(@.type=='Finished')].status",priority=1

// CommittedResourceChangeRequest is the Schema for the committedresourcechangerequests API
type CommittedResourceChangeRequest struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// +required
	Spec CommittedResourceChangeRequestSpec `json:"spec"`

	// +optional
	Status CommittedResourceChangeRequestStatus `json:"status,omitempty,omitzero"`
}

// IsTerminal reports whether the change request has reached a terminal phase.
func (tx *CommittedResourceChangeRequest) IsTerminal() bool {
	return tx.Status.Phase == ChangeRequestPhaseAccepted ||
		tx.Status.Phase == ChangeRequestPhaseRejected ||
		tx.Status.Phase == ChangeRequestPhaseSuperseded
}

// +kubebuilder:object:root=true

// CommittedResourceChangeRequestList contains a list of CommittedResourceChangeRequest
type CommittedResourceChangeRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CommittedResourceChangeRequest `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CommittedResourceChangeRequest{}, &CommittedResourceChangeRequestList{})
}
