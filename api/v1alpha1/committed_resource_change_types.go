// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ChangePhase represents the FSM phase of a CommittedResourceChange.
type ChangePhase string

const (
	// ChangePhasePreparing: change request created; sanity checks and optional snapshot in progress.
	ChangePhasePreparing ChangePhase = "Preparing"
	// ChangePhaseAllocating: CR specs are being written.
	ChangePhaseAllocating ChangePhase = "Allocating"
	// ChangePhaseAwaitingScheduling: CR specs written; waiting for placement success.
	ChangePhaseAwaitingScheduling ChangePhase = "AwaitingScheduling"
	// ChangePhaseRollingBack: a CR was rejected or timed out; restoring pre-change state.
	ChangePhaseRollingBack ChangePhase = "RollingBack"
	// ChangePhaseDryRunCleanup: dry-run would-accept; restoring pre-change state.
	ChangePhaseDryRunCleanup ChangePhase = "DryRunCleanup"
	// ChangePhaseAccepted: terminal; change request committed successfully.
	ChangePhaseAccepted ChangePhase = "Accepted"
	// ChangePhaseRejected: terminal; change request rolled back.
	ChangePhaseRejected ChangePhase = "Rejected"
	// ChangePhaseSuperseded: terminal; abandoned because a newer change request for the same AZ took over.
	ChangePhaseSuperseded ChangePhase = "Superseded"
)

// TxSource identifies the originator of a CommittedResourceChange.
type TxSource string

const (
	// TxSourceAPI: change request created by the change-commitments HTTP API.
	TxSourceAPI TxSource = "api"
	// TxSourceSyncer: change request created by the Limes syncer.
	TxSourceSyncer TxSource = "syncer"
)

const (
	// ChangeConditionProgressing is True while the change request is advancing normally.
	// False + reason indicates a problem while still non-terminal.
	ChangeConditionProgressing = "Progressing"

	// ChangeConditionFinished is True once the change request has reached a terminal phase.
	ChangeConditionFinished = "Finished"

	// Reasons for ChangeConditionProgressing=False (non-terminal problems).
	ChangeReasonStalled        = "Stalled"
	ChangeReasonTimedOut       = "TimedOut"
	ChangeReasonError          = "Error"
	ChangeReasonRetryExhausted = "RetryExhausted"

	// Reasons for ChangeConditionFinished=True.
	ChangeRequestReasonAccepted   = "Accepted"
	ChangeRequestReasonRejected   = "Rejected"
	ChangeRequestReasonSuperseded = "Superseded"
)

// CommittedResourceChangeSpec defines the desired change for a CommittedResourceChange.
type CommittedResourceChangeSpec struct {
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

// CommittedResourceChangeStatus defines the observed state of a CommittedResourceChange.
type CommittedResourceChangeStatus struct {
	// Phase is the current FSM phase of the change request.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="oldSelf == '' || !['Accepted','Rejected','Superseded'].exists(t, t == oldSelf) || self == oldSelf",message="phase cannot transition out of a terminal state"
	Phase ChangePhase `json:"phase,omitempty"`

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
// +kubebuilder:resource:scope=Cluster,shortName=crc
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="AZ",type="string",JSONPath=".spec.availabilityZone"
// +kubebuilder:printcolumn:name="Source",type="string",JSONPath=".spec.source"
// +kubebuilder:printcolumn:name="DryRun",type="boolean",JSONPath=".spec.dryRun",priority=1
// +kubebuilder:printcolumn:name="Progressing",type="string",JSONPath=".status.conditions[?(@.type=='Progressing')].status",priority=1
// +kubebuilder:printcolumn:name="Finished",type="string",JSONPath=".status.conditions[?(@.type=='Finished')].status",priority=1

// CommittedResourceChange is the Schema for the committedresourcechanges API
type CommittedResourceChange struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// +required
	Spec CommittedResourceChangeSpec `json:"spec"`

	// +optional
	Status CommittedResourceChangeStatus `json:"status,omitempty,omitzero"`
}

// IsTerminal reports whether the change request has reached a terminal phase.
func (tx *CommittedResourceChange) IsTerminal() bool {
	return tx.Status.Phase == ChangePhaseAccepted ||
		tx.Status.Phase == ChangePhaseRejected ||
		tx.Status.Phase == ChangePhaseSuperseded
}

// +kubebuilder:object:root=true

// CommittedResourceChangeList contains a list of CommittedResourceChange
type CommittedResourceChangeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CommittedResourceChange `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CommittedResourceChange{}, &CommittedResourceChangeList{})
}
