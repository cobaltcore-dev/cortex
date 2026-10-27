// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TxPhase represents the FSM phase of a CommittedResourceTransaction.
type TxPhase string

const (
	// TxPhasePreparing: transaction created; sanity checks and optional snapshot in progress.
	TxPhasePreparing TxPhase = "Preparing"
	// TxPhaseAllocating: CR specs are being written.
	TxPhaseAllocating TxPhase = "Allocating"
	// TxPhaseAwaitingScheduling: CR specs written; waiting for placement success.
	TxPhaseAwaitingScheduling TxPhase = "AwaitingScheduling"
	// TxPhaseRollingBack: a CR was rejected or timed out; restoring pre-change state.
	TxPhaseRollingBack TxPhase = "RollingBack"
	// TxPhaseDryRunCleanup: dry-run would-accept; restoring pre-change state.
	TxPhaseDryRunCleanup TxPhase = "DryRunCleanup"
	// TxPhaseAccepted: terminal; transaction committed successfully.
	TxPhaseAccepted TxPhase = "Accepted"
	// TxPhaseRejected: terminal; transaction rolled back.
	TxPhaseRejected TxPhase = "Rejected"
	// TxPhaseSuperseded: terminal; abandoned because a newer transaction for the same AZ took over.
	TxPhaseSuperseded TxPhase = "Superseded"
)

// TxSource identifies the originator of a CommittedResourceTransaction.
type TxSource string

const (
	// TxSourceAPI: transaction created by the change-commitments HTTP API.
	TxSourceAPI TxSource = "api"
	// TxSourceSyncer: transaction created by the Limes syncer.
	TxSourceSyncer TxSource = "syncer"
)

const (
	// TxConditionProgressing is True while the transaction is advancing normally.
	// False + reason indicates a problem while still non-terminal.
	TxConditionProgressing = "Progressing"

	// TxConditionFinished is True once the transaction has reached a terminal phase.
	TxConditionFinished = "Finished"

	// Reasons for TxConditionProgressing=False (non-terminal problems).
	TxReasonStalled        = "Stalled"
	TxReasonTimedOut       = "TimedOut"
	TxReasonError          = "Error"
	TxReasonRetryExhausted = "RetryExhausted"

	// Reasons for TxConditionFinished=True.
	TxReasonAccepted   = "Accepted"
	TxReasonRejected   = "Rejected"
	TxReasonSuperseded = "Superseded"
)

// CommittedResourceTransactionSpec defines the desired change for a CommittedResourceTransaction.
type CommittedResourceTransactionSpec struct {
	// CorrelationID is the request/correlation ID.
	// +kubebuilder:validation:Optional
	CorrelationID string `json:"correlationID,omitempty"`

	// AvailabilityZone is the availability zone this transaction is scoped to.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	AvailabilityZone string `json:"availabilityZone"`

	// Source identifies who created this transaction.
	// +kubebuilder:validation:Enum=api;syncer
	// +kubebuilder:validation:Required
	Source TxSource `json:"source"`

	// AllowRejection controls whether the transaction may be rejected and rolled back
	// on placement failure. When false, the reconciler retries until Accepted or Superseded.
	// +kubebuilder:validation:Optional
	AllowRejection bool `json:"allowRejection,omitempty"`

	// DryRun controls whether the transaction commits or only probes schedulability.
	// +kubebuilder:validation:Optional
	DryRun bool `json:"dryRun,omitempty"`

	// NewState maps commitmentUUID to the desired CommittedResourceSpec.
	// A nil value means the corresponding CommittedResource should be deleted.
	// +kubebuilder:validation:Optional
	NewState map[string]*CommittedResourceSpec `json:"newState,omitempty"`
}

// CommittedResourceTransactionStatus defines the observed state of a CommittedResourceTransaction.
type CommittedResourceTransactionStatus struct {
	// Phase is the current FSM phase of the transaction.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="oldSelf == '' || !['Accepted','Rejected','Superseded'].exists(t, t == oldSelf) || self == oldSelf",message="phase cannot transition out of a terminal state"
	Phase TxPhase `json:"phase,omitempty"`

	// SnapshotBefore records the CommittedResource spec state before this transaction.
	// Key is commitmentUUID; nil value means the CR did not exist before (will be deleted on rollback).
	// Populated in Preparing when AllowRejection=true.
	// +kubebuilder:validation:Optional
	SnapshotBefore map[string]*CommittedResourceSpec `json:"snapshotBefore,omitempty"`

	// UpsertedCommitments holds references to CommittedResource CRDs written by this transaction.
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
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="AZ",type="string",JSONPath=".spec.availabilityZone"
// +kubebuilder:printcolumn:name="Source",type="string",JSONPath=".spec.source"
// +kubebuilder:printcolumn:name="DryRun",type="boolean",JSONPath=".spec.dryRun",priority=1
// +kubebuilder:printcolumn:name="Progressing",type="string",JSONPath=".status.conditions[?(@.type=='Progressing')].status",priority=1
// +kubebuilder:printcolumn:name="Finished",type="string",JSONPath=".status.conditions[?(@.type=='Finished')].status",priority=1

// CommittedResourceTransaction is the Schema for the committedresourcetransactions API
type CommittedResourceTransaction struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// +required
	Spec CommittedResourceTransactionSpec `json:"spec"`

	// +optional
	Status CommittedResourceTransactionStatus `json:"status,omitempty,omitzero"`
}

// IsTerminal reports whether the transaction has reached a terminal phase.
func (tx *CommittedResourceTransaction) IsTerminal() bool {
	return tx.Status.Phase == TxPhaseAccepted ||
		tx.Status.Phase == TxPhaseRejected ||
		tx.Status.Phase == TxPhaseSuperseded
}

// +kubebuilder:object:root=true

// CommittedResourceTransactionList contains a list of CommittedResourceTransaction
type CommittedResourceTransactionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CommittedResourceTransaction `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CommittedResourceTransaction{}, &CommittedResourceTransactionList{})
}
