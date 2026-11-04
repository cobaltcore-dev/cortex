// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"slices"
	"sort"

	"github.com/go-logr/logr"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"k8s.io/apimachinery/pkg/api/meta"
)

// FilterFailoverHostOpts configures the failover-host filter.
type FilterFailoverHostOpts struct {
	// Enabled toggles the filter. When false, the filter is a no-op and all
	// hosts pass. This lets the pipeline carry the step while keeping the
	// behavior off until the feature is rolled out.
	Enabled bool `json:"enabled"`
}

func (FilterFailoverHostOpts) Validate() error { return nil }

// FilterFailoverHost keeps ordinary virtual machines off hosts that have a
// failover host reservation, and for evacuations narrows the reserved hosts down
// to the single one this source host should gang-evacuate onto.
//
// For a non-evacuation request it removes every failover host, so no new virtual
// machine lands on a host being drained or kept empty as a gang-evacuation
// landing zone.
//
// For an evacuation request it removes every failover host except the one
// deterministically selected for the request's source host. Keeping only the
// selected target — and leaving the ordinary, non-reserved candidates untouched —
// means the other reserved hosts stay free for a second host failure, while Nova
// can still retry onto an ordinary candidate if the evacuation onto the selected
// target fails. The weigher_failover_host weigher then lifts the selected target
// to the front of the candidate list, applying the same selection rule.
//
// The filter is purely subtractive, so its position in the pipeline does not
// matter: other filters and weighers still run around it.
type FilterFailoverHost struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, FilterFailoverHostOpts]
}

func (s *FilterFailoverHost) Run(traceLog logr.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
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

	var reservations v1alpha1.ReservationList
	if err := s.Client.List(context.Background(), &reservations); err != nil {
		return nil, err
	}

	// For an evacuation, keep the single failover host selected for this source so
	// the gang lands there; for everything else, keep none. The selection is a
	// pure function of the reservation snapshot — among the reservations whose
	// partition includes the source host, prefer the one already in use by this
	// very source (gang-evacuation reuse), never one in use by a different source
	// (that would steal an active landing zone), otherwise the first by target
	// host name — so the weigher_failover_host weigher reaches the same target
	// over the same snapshot without coordinating.
	var keep string
	if intent, err := request.GetIntent(); err == nil && intent == api.EvacuateIntent {
		if sourceHost, err := request.Spec.Data.GetSchedulerHintStr("source_host"); err == nil && sourceHost != "" {
			var matching []v1alpha1.Reservation
			for _, res := range reservations.Items {
				if res.Spec.Type != v1alpha1.ReservationTypeFailoverHost || res.Spec.FailoverHostReservation == nil {
					continue
				}
				if slices.Contains(res.Spec.FailoverHostReservation.SourceHosts, sourceHost) {
					matching = append(matching, res)
				}
			}
			sort.Slice(matching, func(i, j int) bool {
				return matching[i].Spec.TargetHost < matching[j].Spec.TargetHost
			})
			for i := range matching {
				res := &matching[i]
				if !meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostInUse) {
					if keep == "" {
						keep = res.Spec.TargetHost
					}
					continue
				}
				if res.Status.FailoverHostReservation != nil &&
					res.Status.FailoverHostReservation.SourceHost == sourceHost {
					keep = res.Spec.TargetHost
					break
				}
			}
		}
	}

	for _, reservation := range reservations.Items {
		if reservation.Spec.Type != v1alpha1.ReservationTypeFailoverHost {
			continue
		}
		// Block both the desired (TargetHost) and actual (Status.Host) host to
		// cover the window while the reservation is being placed. A reservation
		// only exists from the Designated stage onward (the controller deletes
		// Revoked/Used ones), so its presence is enough to keep new virtual
		// machines off the host.
		for _, host := range []string{reservation.Spec.TargetHost, reservation.Status.Host} {
			if host == "" || host == keep {
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
	Index["filter_failover_host"] = func() NovaFilter { return &FilterFailoverHost{} }
}
