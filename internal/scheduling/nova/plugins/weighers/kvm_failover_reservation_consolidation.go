// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"context"
	"log/slog"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

// Options for the KVM failover reservation consolidation weigher.
type KVMFailoverReservationConsolidationOpts struct {
	// Weight multiplier for the total failover reservation count per host (consolidation signal).
	// Higher values more aggressively pack failover reservations onto fewer hosts.
	// Default: 1.0
	TotalCountWeight *float64 `json:"totalCountWeight,omitempty"`
	// Penalty multiplier for same-spec reservation count per host (diversity signal).
	// Higher values more aggressively avoid clustering reservations of the same size on one host.
	// Should be less than TotalCountWeight to ensure consolidation is the primary goal.
	// Default: 0.1
	SameSpecPenalty *float64 `json:"sameSpecPenalty,omitempty"`
}

func (o KVMFailoverReservationConsolidationOpts) Validate() error {
	return nil
}

func (o KVMFailoverReservationConsolidationOpts) GetTotalCountWeight() float64 {
	if o.TotalCountWeight == nil {
		return 1.0
	}
	return *o.TotalCountWeight
}

func (o KVMFailoverReservationConsolidationOpts) GetSameSpecPenalty() float64 {
	if o.SameSpecPenalty == nil {
		return 0.1
	}
	return *o.SameSpecPenalty
}

// KVMFailoverReservationConsolidationStep weighs hosts for failover reservation placement.
// It encourages consolidating failover reservations onto as few hosts as possible (primary goal),
// while preferring hosts with fewer reservations of the same ResourceGroup (secondary tiebreaker).
//
// The ResourceGroup is passed via the scheduler hint "_cortex_resource_group" and compared against
// each existing reservation's Spec.FailoverReservation.ResourceGroup. This groups reservations
// by flavor group (or individual flavor name when no group exists).
//
// Score formula (normalized by total reservation count T):
//
//	score = (totalCountWeight / T) × hostCount - (sameSpecPenalty / T) × sameGroupCount
//
// This produces bounded output (~0 to 1) that plays nicely with other weighers.
type KVMFailoverReservationConsolidationStep struct {
	lib.BaseWeigher[api.ExternalSchedulerRequest, KVMFailoverReservationConsolidationOpts]
}

// Run the weigher step.
// For reserve_for_failover requests, hosts are scored based on existing failover reservation density
// and same-spec diversity. For all other request types, this weigher has no effect.
func (s *KVMFailoverReservationConsolidationStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)

	intent, err := request.GetIntent()
	if err != nil || intent != api.ReserveForFailoverIntent {
		traceLog.Info("skipping failover reservation consolidation weigher for non-failover-reservation request")
		return result, nil //nolint:nilerr // intentionally skip weigher on error
	}

	// Extract the resource group from the scheduler hint.
	// This identifies which "spec group" the incoming reservation belongs to.
	// If the hint is missing, requestResourceGroup will be empty and the same-group penalty is skipped.
	requestResourceGroup, _ := request.Spec.Data.GetSchedulerHintStr(api.HintKeyResourceGroup) //nolint:errcheck // missing hint is fine, same-group penalty is simply skipped

	// Fetch all reservations.
	var reservations v1alpha1.ReservationList
	if err := s.Client.List(context.Background(), &reservations); err != nil {
		return nil, err
	}

	// Count failover reservations per host, and same-group reservations per host.
	totalPerHost := make(map[string]float64)
	sameGroupPerHost := make(map[string]float64)
	totalReservations := 0

	for _, reservation := range reservations.Items {
		// Only consider active failover reservations (Ready condition is True).
		if !reservation.IsReady() {
			continue
		}
		if reservation.Spec.Type != v1alpha1.ReservationTypeFailover {
			continue
		}

		host := reservation.Status.Host
		if host == "" {
			continue
		}

		totalReservations++
		totalPerHost[host]++

		// Check if this reservation belongs to the same resource group as the request.
		if requestResourceGroup != "" && reservation.Spec.FailoverReservation != nil &&
			reservation.Spec.FailoverReservation.ResourceGroup == requestResourceGroup {
			sameGroupPerHost[host]++
		}
	}

	// If there are no failover reservations, the weigher has no information to act on.
	if totalReservations == 0 {
		traceLog.Info("no active failover reservations found, skipping consolidation weigher")
		return result, nil
	}

	totalCountWeight := s.Options.GetTotalCountWeight()
	sameSpecPenalty := s.Options.GetSameSpecPenalty()
	t := float64(totalReservations)

	for _, host := range request.Hosts {
		hostTotal := totalPerHost[host.ComputeHost]
		hostSameGroup := sameGroupPerHost[host.ComputeHost]

		// Normalized score: bounded output for compatibility with other weighers.
		score := (totalCountWeight/t)*hostTotal - (sameSpecPenalty/t)*hostSameGroup

		result.Activations[host.ComputeHost] = score
		traceLog.Info("calculated failover consolidation score for host",
			"host", host.ComputeHost,
			"totalOnHost", hostTotal,
			"sameGroupOnHost", hostSameGroup,
			"resourceGroup", requestResourceGroup,
			"totalReservations", totalReservations,
			"score", score)
	}

	return result, nil
}

func init() {
	Index["kvm_failover_reservation_consolidation"] = func() NovaWeigher {
		return &KVMFailoverReservationConsolidationStep{}
	}
}
