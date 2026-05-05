// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import "github.com/cobaltcore-dev/cortex/api/v1alpha1"

// Options configure the behavior of a single pipeline run at call time.
// These are distinct from per-step YAML options (FilterWeigherPipelineStepOpts),
// which are static and set when the pipeline is initialized.
//
// Consumed by steps: ReadOnly, LockReservations, AssumeEmptyHosts, IgnoredReservationTypes.
// Consumed by the controller after pipeline.Run(): RecordHistory, CreateInflight.
type Options struct {
	// ReadOnly means the pipeline could run without using the mutex, i.e. concurrent runs are ok.
	ReadOnly bool
	// LockReservations prevents reservation unlocking, e.g. in the capacity filter.
	// Set when finding hosts for new reservations (failover, CR) to see true available capacity.
	LockReservations bool
	// AssumeEmptyHosts treats all hosts as having no running VMs.
	AssumeEmptyHosts bool
	// IgnoredReservationTypes lists reservation types the capacity filter skips entirely.
	IgnoredReservationTypes []v1alpha1.ReservationType
	// MaxCandidates limits the number of hosts returned after weighing. 0 means no limit.
	MaxCandidates int

	// RecordHistory records the placement decision in placement history.
	// Replaces pipeline.Spec.CreateHistory once pipelines consolidate.
	RecordHistory bool
	// CreateInflight creates pessimistic blocking reservations for all returned candidates.
	CreateInflight bool
}
