// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"slices"

	"github.com/go-logr/logr"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

// FilterNoFailoverHostOpts configures the no-failover-host filter.
type FilterNoFailoverHostOpts struct {
	// Enabled toggles the filter. When false, the filter is a no-op and all
	// hosts pass. This lets the pipeline carry the step while keeping the
	// behavior off until the feature is rolled out.
	Enabled bool `json:"enabled"`
}

func (FilterNoFailoverHostOpts) Validate() error { return nil }

// FilterNoFailoverHost blocks hosts that have a failover host reservation, so no
// new virtual machines are placed on a host that is being drained or kept empty
// as a gang-evacuation landing zone.
//
// This only applies to non-evacuation requests. Evacuation requests are routed
// onto reserved hosts separately, after the pipeline, by the failover host
// Matcher (see reservations/failoverhost/matcher.go), so this filter leaves them
// untouched.
//
// The filter is purely subtractive, so its position in the pipeline does not
// matter: other filters and weighers still run around it.
type FilterNoFailoverHost struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, FilterNoFailoverHostOpts]
}

func (s *FilterNoFailoverHost) Run(traceLog logr.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)
	if !s.Options.Enabled {
		return result, nil
	}

	// Honor the call-time ignored reservation types. When failover host
	// reservations are ignored — as the path finder probe does (see
	// reservations/failoverhost/pathfinding.go) — block nothing so reserved
	// hosts stay visible to their own partition.
	if slices.Contains(request.GetOptions().IgnoredReservationTypes, v1alpha1.ReservationTypeFailoverHost) {
		traceLog.V(1).Info("failover host reservations ignored, not blocking any hosts")
		return result, nil
	}

	// Evacuations are routed onto reserved hosts by the Matcher, not blocked here.
	if intent, err := request.GetIntent(); err == nil && intent == api.EvacuateIntent {
		traceLog.V(1).Info("evacuation request, leaving hosts untouched for the failover host matcher")
		return result, nil
	}

	var reservations v1alpha1.ReservationList
	if err := s.Client.List(context.Background(), &reservations); err != nil {
		return nil, err
	}
	for _, reservation := range reservations.Items {
		if reservation.Spec.Type != v1alpha1.ReservationTypeFailoverHost {
			continue
		}
		// Block both the desired (TargetHost) and actual (Status.Host) host, as
		// the capacity filter does, to cover the window while the reservation is
		// being placed. A reservation only exists from the Designated stage
		// onward (the controller deletes Revoked/Used ones), so its presence is
		// enough to keep new virtual machines off the host.
		for _, host := range []string{reservation.Spec.TargetHost, reservation.Status.Host} {
			if host == "" {
				continue
			}
			delete(result.Activations, host) // noop if host is not in the map
			traceLog.V(1).Info("filtering out failover host reservation target",
				"host", host, "reservation", reservation.Name)
		}
	}
	return result, nil
}

func init() {
	Index["filter_no_failover_host"] = func() NovaFilter { return &FilterNoFailoverHost{} }
}
