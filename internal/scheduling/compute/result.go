// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

// SchedulerResult is the well-defined output of a scheduling pipeline run:
// ordered hosts, the target host, and the input/output weights plus per-step
// activations used to rank them. It is an alias of lib.PipelineResult, the
// domain-neutral type the generic pipeline returns.
type SchedulerResult = lib.PipelineResult

// MatchedReservation records a reservation that matched a candidate host during
// the run, for observability and downstream consumers.
type MatchedReservation struct {
	// Host is the compute host the reservation applies to.
	Host string
	// ReservationName is the name of the matched Reservation resource.
	ReservationName string
	// Type is the reservation type (e.g. inflight, committed-resource, failover).
	Type v1alpha1.ReservationType
}

// RunOutcome bundles everything the pipeline produces for a scheduling run: the
// pipeline result and any matched reservations. The human-readable explanation
// is NOT part of the outcome — it is a concern of writing the decision onto the
// scheduled resource (e.g. the Nova VM's CortexSpec), so the relevant side
// effect generates it from the Result via lib.GenerateExplanation.
type RunOutcome struct {
	// Result is the pipeline output (ordered hosts, target host, weights, steps).
	Result SchedulerResult
	// MatchedReservations lists reservations that matched candidate hosts.
	MatchedReservations []MatchedReservation
}
