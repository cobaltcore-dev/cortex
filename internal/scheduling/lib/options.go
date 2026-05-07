// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"errors"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

// Options configure the behavior of a single pipeline run at call time.
// These are distinct from per-step YAML options (FilterWeigherPipelineStepOpts),
// which are static and set when the pipeline is initialized.
type Options struct {
	// ReadOnly means the pipeline run does not modify shared scheduling state (reservations,
	// history, inflight records). Concurrent read-only runs are safe under a shared read lock.
	// Note: the controller may still write the Decision status after Run() regardless of this flag.
	ReadOnly bool `json:"read_only,omitempty"`
	// LockReservations prevents reservation unlocking, e.g. in the capacity filter.
	// Set when finding hosts for new reservations (failover, CR) to see true available capacity.
	LockReservations bool `json:"lock_reservations,omitempty"`
	// AssumeEmptyHosts treats all hosts as having no running VMs.
	AssumeEmptyHosts bool `json:"assume_empty_hosts,omitempty"`
	// IgnoredReservationTypes lists reservation types the capacity filter skips entirely.
	IgnoredReservationTypes []v1alpha1.ReservationType `json:"ignored_reservation_types,omitempty"`
	// MaxCandidates limits the number of hosts returned after weighing. 0 means no limit.
	MaxCandidates int `json:"max_candidates,omitempty"`

	// SkipHistory skips recording the placement decision in placement history.
	// Defaults to false so Nova requests record history without needing to set this explicitly.
	SkipHistory bool `json:"skip_history,omitempty"`
	// CreateInflight creates pessimistic blocking reservations for all returned candidates.
	CreateInflight bool `json:"create_inflight,omitempty"`
}

// Validate checks for mutually exclusive or inconsistent option combinations.
func (o Options) Validate() error {
	if o.ReadOnly && !o.SkipHistory {
		return errors.New("read-only runs must not write scheduling history: set SkipHistory=true")
	}
	if o.ReadOnly && o.CreateInflight {
		return errors.New("ReadOnly and CreateInflight are mutually exclusive: read-only runs must not create inflight reservations")
	}
	return nil
}
