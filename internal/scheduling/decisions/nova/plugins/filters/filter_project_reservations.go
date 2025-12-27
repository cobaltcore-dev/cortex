// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type FilterProjectReservations struct {
	lib.BaseStep[api.ExternalSchedulerRequest, lib.EmptyStepOpts]
}

func (s *FilterProjectReservations) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.StepResult, error) {
	result := s.PrepareResult(request)
	var reservations v1alpha1.ReservationList
	ctx := context.Background()
	if err := s.Client.List(ctx, &reservations); err != nil {
		return nil, err
	}

	hostHasReservation := make(map[string]bool)

	for _, reservation := range reservations.Items {
		if reservation.Status.Phase != v1alpha1.ReservationStatusPhaseActive {
			continue // Only consider active reservations.
		}
		if reservation.Spec.Scheduler.CortexNova == nil {
			continue // Not handled by us.
		}
		// If the requested vm matches this reservation, free the resources.
		if reservation.Spec.Scheduler.CortexNova.ProjectID == request.Spec.Data.ProjectID &&
			reservation.Spec.Scheduler.CortexNova.FlavorName == request.Spec.Data.Flavor.Data.Name {
			hostHasReservation[reservation.Status.Host] = true
			break
		}
	}
	for host := range result.Activations {
		// Filter out hosts that do not have a matching reservation.
		if _, ok := hostHasReservation[host]; !ok {
			delete(result.Activations, host)
			traceLog.Debug(
				"removing host with unknown capacity",
				"host", host,
			)
		}
	}
	return result, nil
}
