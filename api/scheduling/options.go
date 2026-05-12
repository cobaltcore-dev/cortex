// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduling

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
	ReadOnly bool `json:"read_only,omitempty"`
	// LockReservations prevents reservation unlocking, i.e. considering those as unavailable resources.
	LockReservations bool `json:"lock_reservations,omitempty"`
	// AssumeEmptyHosts ignores running instances on hosts, considering them as empty.
	AssumeEmptyHosts bool `json:"assume_empty_hosts,omitempty"`
	// IgnoredReservationTypes lists reservation types whose reserved capacity the capacity filter does not block.
	IgnoredReservationTypes []v1alpha1.ReservationType `json:"ignored_reservation_types,omitempty"`
	// MaxCandidates limits the number of hosts returned after weighing. 0 means no limit.
	MaxCandidates int `json:"max_candidates,omitempty"`

	// SkipHistory skips recording the placement decision in placement history.
	SkipHistory bool `json:"skip_history,omitempty"`
	// SkipInflight skips creating pessimistic blocking reservations for returned candidates.
	SkipInflight bool `json:"skip_inflight,omitempty"`
}

// Validate checks for mutually exclusive or inconsistent option combinations.
func (o Options) Validate() error {
	if o.ReadOnly && !o.SkipHistory {
		return errors.New("read-only runs must not write scheduling history: set SkipHistory=true")
	}
	if o.ReadOnly && !o.SkipInflight {
		return errors.New("read-only runs cannot create inflight reservations")
	}
	return nil
}
