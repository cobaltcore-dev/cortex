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

// Matcher confirms an evacuation's gang target after the scheduling pipeline has
// run and pins the source host to that target, implementing gang-evacuation.
//
// The candidate shaping — removing the other reserved hosts and lifting the
// selected failover host to the front — is done inside the pipeline by the
// filter_failover_host filter and the weigher_failover_host weigher. The Matcher
// does not touch the candidate list. Its sole job is the stateful write: once the
// pipeline has settled on a target host, it marks the matching reservation InUse
// so that subsequent evacuations from the same source host deterministically land
// on the same target.
//
// It runs from inside the pipeline controller's exclusive lock, on write runs
// only, so the InUse write is serialized against — and visible to — subsequent
// evacuation requests from the same source host. The write is committed only once
// (on the first evacuation from a given source); later requests see the
// already-InUse reservation (the filter/weigher keep selecting it) and so land on
// the same target.
//
// The Matcher only ever advances a reservation to InUse. Moving it back out
// (to Active/Used once the evacuation completes) is owned by the failover host
// lifecycle reconciler, based on the real workload present on the host.
type Matcher struct {
	client.Client
	// Monitor records the evacuation-without-reservation metric. May be nil.
	Monitor *MatcherMonitor
}

// ConfirmAndMarkInUse inspects the pipeline's ordered candidates for an evacuation
// request and, if the top candidate is the failover host reservation selected for
// the request's source host, marks that reservation InUse.
//
// It is a no-op for non-evacuation requests and for evacuations without a source
// host. When no failover host reservation matches the source host — or the
// selected one did not end up as the top candidate (it was filtered out by other
// constraints, so the evacuation falls back to an ordinary host) — it records the
// evacuation-without-reservation metric and writes nothing.
func (m *Matcher) ConfirmAndMarkInUse(ctx context.Context, request api.ExternalSchedulerRequest, orderedHosts []string) error {
	log := logf.FromContext(ctx)

	intent, err := request.GetIntent()
	if err != nil || intent != api.EvacuateIntent {
		return nil //nolint:nilerr // non-evacuation requests are not matched
	}
	sourceHost, err := request.Spec.Data.GetSchedulerHintStr("source_host")
	if err != nil || sourceHost == "" {
		log.V(1).Info("no source_host hint on evacuation, nothing to confirm")
		return nil //nolint:nilerr // without a source host there is nothing to match
	}

	var reservations v1alpha1.ReservationList
	if err := m.List(ctx, &reservations); err != nil {
		return err
	}
	// Re-derive the selected reservation with the same deterministic rule the
	// filter and weigher used over the same snapshot, so we confirm the one they
	// shaped the candidates around: among the reservations whose partition
	// includes the source host, prefer the one already in use by this very source
	// (gang-evacuation reuse), never one in use by a different source (that would
	// steal an active landing zone), otherwise the first by target host name.
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
	var selected *v1alpha1.Reservation
	for i := range matching {
		res := &matching[i]
		if !meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostInUse) {
			if selected == nil {
				selected = res
			}
			continue
		}
		if res.Status.FailoverHostReservation != nil &&
			res.Status.FailoverHostReservation.SourceHost == sourceHost {
			selected = res
			break
		}
	}
	if selected == nil {
		// Either no reservation can rescue this source, or every matching one is
		// already absorbing another source host's gang. Surface it either way.
		log.V(1).Info("no selectable failover host reservation for source host", "sourceHost", sourceHost)
		m.Monitor.RecordEvacuationWithoutReservation(request.Spec.Data.AvailabilityZone)
		return nil
	}

	// Only pin when the pipeline actually settled on the selected target. If it is
	// not the top candidate (filtered out by other constraints, so the evacuation
	// falls back to an ordinary host), we must not leave a dangling InUse.
	if len(orderedHosts) == 0 || orderedHosts[0] != selected.Spec.TargetHost {
		log.V(1).Info("selected failover host is not the top candidate, not pinning",
			"sourceHost", sourceHost, "targetHost", selected.Spec.TargetHost)
		m.Monitor.RecordEvacuationWithoutReservation(request.Spec.Data.AvailabilityZone)
		return nil
	}
	// Already in use by this source (gang-evacuation reuse): nothing to write.
	if meta.IsStatusConditionTrue(selected.Status.Conditions, v1alpha1.ConditionTypeFailoverHostInUse) {
		log.V(1).Info("failover host reservation already in use for this source, reusing",
			"sourceHost", sourceHost, "reservation", selected.Name, "targetHost", selected.Spec.TargetHost)
		return nil
	}
	if err := m.markInUse(ctx, selected, sourceHost); err != nil {
		return err
	}
	log.V(1).Info("pinned failover host reservation for evacuation",
		"sourceHost", sourceHost, "reservation", selected.Name, "targetHost", selected.Spec.TargetHost)
	return nil
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
