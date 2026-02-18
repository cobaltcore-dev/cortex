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
	// Default: 1.0
	FailoverHostWeight *float64 `json:"failoverHostWeight,omitempty"`
	// Weight to assign to hosts that don't match a failover reservation.
	// Default: 0.1
	DefaultHostWeight *float64 `json:"defaultHostWeight,omitempty"`
}

func (o KVMFailoverEvacuationOpts) Validate() error {
	return nil
}

func (o KVMFailoverEvacuationOpts) GetFailoverHostWeight() float64 {
	if o.FailoverHostWeight == nil {
		return 1.0
	}
	return *o.FailoverHostWeight
}

func (o KVMFailoverEvacuationOpts) GetDefaultHostWeight() float64 {
	if o.DefaultHostWeight == nil {
		return 0.1
	}
	return *o.DefaultHostWeight
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
		traceLog.Info("skipping failover weigher for non-evacuation request")
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
				traceLog.Info("found failover reservation for VM",
					"reservation", reservation.Name,
					"host", reservation.Status.Host,
					"instanceUUID", instanceUUID)
			}
		}
	}

	failoverWeight := s.Options.GetFailoverHostWeight()
	defaultWeight := s.Options.GetDefaultHostWeight()

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
