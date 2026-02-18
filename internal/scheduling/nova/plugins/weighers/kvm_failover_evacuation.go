// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"context"
	"log/slog"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"k8s.io/apimachinery/pkg/api/meta"
)

// Options for the KVM failover evacuation weigher.
type KVMFailoverEvacuationOpts struct {
	// Weight to assign to hosts that match a failover reservation for the VM.
	FailoverHostWeight float64 `json:"failoverHostWeight"`
	// Weight to assign to hosts that don't match a failover reservation.
	DefaultHostWeight float64 `json:"defaultHostWeight"`
}

func (o KVMFailoverEvacuationOpts) Validate() error {
	return nil
}

// KVMFailoverEvacuationStep weighs hosts based on failover reservations.
// Hosts that match a failover reservation for the VM being scheduled get a higher weight,
// encouraging placement on pre-reserved failover capacity.
type KVMFailoverEvacuationStep struct {
	lib.BaseWeigher[api.ExternalSchedulerRequest, KVMFailoverEvacuationOpts]
}

// Run the weigher step.
// For evacuation requests, hosts matching a failover reservation where the VM is in Allocations get a higher weight.
// For non-evacuation requests (e.g., live migration, rebuild), this weigher has no effect.
func (s *KVMFailoverEvacuationStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)

	intent, err := request.GetIntent()
	if err != nil || intent != api.EvacuateIntent {
		traceLog.Debug("skipping failover weigher for non-evacuation request")
		return result, nil //nolint:nilerr // intentionally skip weigher on error
	}

	instanceUUID := request.Spec.Data.InstanceUUID

	// Fetch all reservations
	var reservations v1alpha1.ReservationList
	if err := s.Client.List(context.Background(), &reservations); err != nil {
		return nil, err
	}

	// Build a map of hosts that have failover reservations for this VM
	failoverHosts := make(map[string]bool)
	for _, reservation := range reservations.Items {
		// Only consider active failover reservations (Ready condition is True)
		readyCondition := meta.FindStatusCondition(reservation.Status.Conditions, v1alpha1.ReservationConditionReady)
		if readyCondition == nil || readyCondition.Status != "True" {
			continue
		}
		if reservation.Spec.Type != v1alpha1.ReservationTypeFailover {
			continue
		}
		if reservation.Spec.FailoverReservation == nil {
			continue // Not a valid failover reservation
		}

		// Check if this VM is in the Allocations map of this failover reservation
		if reservation.Status.FailoverReservation != nil {
			if _, ok := reservation.Status.FailoverReservation.Allocations[instanceUUID]; ok {
				failoverHosts[reservation.Status.Host] = true
				traceLog.Debug("found failover reservation for VM",
					"reservation", reservation.Name,
					"host", reservation.Status.Host,
					"instanceUUID", instanceUUID)
			}
		}
	}

	// Assign weights based on whether the host has a failover reservation for this VM
	failoverWeight := s.Options.FailoverHostWeight
	if failoverWeight == 0 {
		failoverWeight = 1.0 // Default to 1.0 if not configured
	}
	defaultWeight := s.Options.DefaultHostWeight
	if defaultWeight == 0 {
		defaultWeight = 0.1 // Default to small constant if not configured
	}

	for _, host := range request.Hosts {
		if failoverHosts[host.ComputeHost] {
			result.Activations[host.ComputeHost] = failoverWeight
			traceLog.Info("assigning failover weight to host",
				"host", host.ComputeHost,
				"weight", failoverWeight,
				"instanceUUID", instanceUUID)
		} else {
			result.Activations[host.ComputeHost] = defaultWeight
		}
	}

	return result, nil
}

func init() {
	Index["kvm_failover_evacuation"] = func() NovaWeigher {
		return &KVMFailoverEvacuationStep{}
	}
}
