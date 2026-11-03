// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"context"
	"slices"
	"sort"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Matcher routes an evacuation request onto a failover host reservation and
// pins the source host to a single target, implementing gang-evacuation.
//
// It is called after the scheduling pipeline has produced its ordered
// candidates, from inside the pipeline controller's exclusive lock, so that the
// InUse status write below is serialized against — and visible to — subsequent
// evacuation requests from the same source host. The write is committed only
// once (on the first evacuation from a given source); later requests reuse the
// already-InUse reservation and so land on the same target.
//
// The Matcher only ever advances a reservation to InUse. Moving it back out
// (to Active/Used once the evacuation completes) is owned by the failover host
// lifecycle reconciler, based on the real workload present on the host.
type Matcher struct {
	client.Client
	// Monitor records the evacuation-without-reservation metric. May be nil.
	Monitor *MatcherMonitor
}

// Match returns the candidate hosts to use for this request. For a
// non-evacuation request, or an evacuation the Matcher cannot or should not
// reroute, it returns orig unchanged. For an evacuation it finds the failover
// host reservations whose SourceHosts include the request's source host and
// returns the chosen failover host as the sole candidate, writing the InUse
// condition when it pins a fresh one.
//
// Fallbacks preserve a valid placement: if no reservation matches, or the
// chosen host is not among orig (so it conflicts with other scheduling
// constraints), orig is returned unchanged so the VM still has candidates.
func (m *Matcher) Match(ctx context.Context, request api.ExternalSchedulerRequest, orig []string) ([]string, error) {
	log := logf.FromContext(ctx)

	intent, err := request.GetIntent()
	if err != nil || intent != api.EvacuateIntent {
		return orig, nil //nolint:nilerr // non-evacuation requests are not rerouted
	}
	sourceHost, err := request.Spec.Data.GetSchedulerHintStr("source_host")
	if err != nil || sourceHost == "" {
		log.V(1).Info("no source_host hint on evacuation, leaving candidates untouched")
		return orig, nil //nolint:nilerr // without a source host there is nothing to match
	}

	var reservations v1alpha1.ReservationList
	if err := m.List(ctx, &reservations); err != nil {
		return nil, err
	}
	// Collect the failover host reservations whose partition includes the source
	// host, i.e. the reservations this host may evacuate onto.
	var matching []v1alpha1.Reservation
	for _, res := range reservations.Items {
		if res.Spec.Type != v1alpha1.ReservationTypeFailoverHost || res.Spec.FailoverHostReservation == nil {
			continue
		}
		if slices.Contains(res.Spec.FailoverHostReservation.SourceHosts, sourceHost) {
			matching = append(matching, res)
		}
	}
	if len(matching) == 0 {
		log.V(1).Info("no failover host reservation for source host, falling back to ordinary candidates",
			"sourceHost", sourceHost)
		m.Monitor.RecordEvacuationWithoutReservation(request.Spec.Data.AvailabilityZone)
		return orig, nil
	}

	// Sort deterministically by target host so selection is stable and testable;
	// the InUse write below is what makes a fresh choice binding for subsequent
	// requests from the same source.
	sort.Slice(matching, func(i, j int) bool {
		return matching[i].Spec.TargetHost < matching[j].Spec.TargetHost
	})

	// Classify each candidate reservation by its InUse status. A reservation can
	// legitimately rescue many source hosts (the planner lists every partition
	// peer), so more than one source may evacuate concurrently: one reservation
	// per source. We must therefore (a) reuse the reservation already in use by
	// *this* source, and (b) never pin a reservation already in use by a
	// *different* source, which would steal an active landing zone.
	var free *v1alpha1.Reservation
	for i := range matching {
		res := &matching[i]
		if !meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostInUse) {
			if free == nil {
				free = res
			}
			continue
		}
		// In use: reuse it only if it is in use by this very source host, which
		// is the gang-evacuation path — the whole gang lands on the same target.
		if res.Status.FailoverHostReservation != nil &&
			res.Status.FailoverHostReservation.SourceHost == sourceHost {
			log.V(1).Info("reusing in-use failover host reservation for gang-evacuation",
				"sourceHost", sourceHost, "reservation", res.Name, "targetHost", res.Spec.TargetHost)
			return m.pick(*res, orig), nil
		}
	}

	// None already in use by this source. Pin the first free reservation.
	if free == nil {
		// Every matching reservation is already absorbing another source host's
		// gang. Fall back to ordinary candidates rather than stealing a landing
		// zone, and surface it like the no-reservation case.
		log.V(1).Info("all failover host reservations for source host are in use by other sources, falling back",
			"sourceHost", sourceHost)
		m.Monitor.RecordEvacuationWithoutReservation(request.Spec.Data.AvailabilityZone)
		return orig, nil
	}
	if err := m.markInUse(ctx, free, sourceHost); err != nil {
		return nil, err
	}
	log.V(1).Info("pinned failover host reservation for evacuation",
		"sourceHost", sourceHost, "reservation", free.Name, "targetHost", free.Spec.TargetHost)
	return m.pick(*free, orig), nil
}

// pick returns the reservation's target host as the sole candidate, unless that
// host is not among the pipeline's candidates (it conflicts with other
// scheduling constraints), in which case it falls back to the original set.
func (m *Matcher) pick(res v1alpha1.Reservation, orig []string) []string {
	if slices.Contains(orig, res.Spec.TargetHost) {
		return []string{res.Spec.TargetHost}
	}
	return orig
}

// markInUse sets the InUse condition and records the source host on the
// reservation status.
func (m *Matcher) markInUse(ctx context.Context, res *v1alpha1.Reservation, sourceHost string) error {
	old := res.DeepCopy()
	meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
		Type:    v1alpha1.ConditionTypeFailoverHostInUse,
		Status:  metav1.ConditionTrue,
		Reason:  "EvacuationStarted",
		Message: "a gang evacuation from " + sourceHost + " is underway",
	})
	if res.Status.FailoverHostReservation == nil {
		res.Status.FailoverHostReservation = &v1alpha1.FailoverHostReservationStatus{}
	}
	res.Status.FailoverHostReservation.SourceHost = sourceHost
	return m.Status().Patch(ctx, res, client.MergeFrom(old))
}
