// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"context"
	"slices"
	"sort"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

// failoverTargetWeight is the activation assigned to the failover host a source
// host should gang-evacuate onto. It is deliberately far larger than any other
// weigher's contribution: the pipeline squashes each step with tanh (bounding it
// to ~[-1, 1]) before summing, so a value this large saturates to ~+1 and, being
// the only host lifted at all, is guaranteed to sort to the front of the
// candidate list. That makes the selected target Nova's first choice while the
// ordinary candidates keep their relative order behind it as retry fallbacks.
const failoverTargetWeight = 1e6

// WeigherFailoverHostOpts configures the failover-host weigher.
type WeigherFailoverHostOpts struct {
	// Enabled toggles the weigher. When false, the weigher is a no-op and has no
	// effect on any host's weight.
	Enabled bool `json:"enabled"`
}

func (WeigherFailoverHostOpts) Validate() error { return nil }

// WeigherFailoverHost lifts the failover host an evacuation should land on to the
// front of the candidate list, by giving it a dominating weight.
//
// It selects the target with the same deterministic rule as the
// filter_failover_host filter (which has already removed the other reserved
// hosts), so the two agree without coordinating: both run over the same
// reservation snapshot in one pipeline execution. The filter keeps only the
// selected target among the reserved hosts; this weigher then ensures that target
// outranks the retained ordinary candidates.
type WeigherFailoverHost struct {
	lib.BaseWeigher[api.ExternalSchedulerRequest, WeigherFailoverHostOpts]
}

func (s *WeigherFailoverHost) Run(traceLog logr.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)
	if !s.Options.Enabled {
		return result, nil
	}
	intent, err := request.GetIntent()
	if err != nil || intent != api.EvacuateIntent {
		return result, nil //nolint:nilerr // non-evacuation requests are not weighted
	}
	sourceHost, err := request.Spec.Data.GetSchedulerHintStr("source_host")
	if err != nil || sourceHost == "" {
		return result, nil //nolint:nilerr // without a source host there is nothing to weigh
	}

	var reservations v1alpha1.ReservationList
	if err := s.Client.List(context.Background(), &reservations); err != nil {
		return nil, err
	}

	// Select the one failover host this source should gang-evacuate onto, with the
	// same deterministic rule the filter_failover_host filter used over the same
	// snapshot: among the reservations whose partition includes the source host,
	// prefer the one already in use by this very source (gang-evacuation reuse),
	// never one in use by a different source, otherwise the first by target host
	// name.
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
	var target string
	for i := range matching {
		res := &matching[i]
		if !meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostInUse) {
			if target == "" {
				target = res.Spec.TargetHost
			}
			continue
		}
		if res.Status.FailoverHostReservation != nil &&
			res.Status.FailoverHostReservation.SourceHost == sourceHost {
			target = res.Spec.TargetHost
			break
		}
	}
	if target == "" {
		return result, nil
	}
	if _, ok := result.Activations[target]; !ok {
		// The selected target conflicts with other scheduling constraints and was
		// filtered out, so there is nothing to lift.
		traceLog.V(1).Info("selected failover host not among candidates, not weighing",
			"sourceHost", sourceHost, "targetHost", target)
		return result, nil
	}
	result.Activations[target] = failoverTargetWeight
	stats := s.PrepareStats(request, "failover host target [bool]")
	stats.Hosts[target] = 1
	result.Statistics["failover host target"] = stats
	traceLog.V(1).Info("weighting failover host target to the front",
		"sourceHost", sourceHost, "targetHost", target)
	return result, nil
}

func init() {
	Index["weigher_failover_host"] = func() NovaWeigher { return &WeigherFailoverHost{} }
}
