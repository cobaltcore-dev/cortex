// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	maxHostsInExplanation = 10
	maxHistoryEntries     = 10
	maxHostsInOrderedList = 3
)

// joinHostsCapped joins up to max host names. If hosts exceeds max, it appends
// a count of the omitted entries, e.g. "host-a, host-b (and 48 more)".
func joinHostsCapped(hosts []string, maxHosts int) string {
	if len(hosts) <= maxHosts {
		return strings.Join(hosts, ", ")
	}
	return fmt.Sprintf("%s (and %d more)", strings.Join(hosts[:maxHosts], ", "), len(hosts)-maxHosts)
}

func getName(schedulingDomain v1alpha1.SchedulingDomain, resourceID string) string {
	return fmt.Sprintf("%s-%s", schedulingDomain, resourceID)
}

// generateSimplifiedExplanation produces a human-readable explanation from a
// decision result. On failure it includes the error. On success it describes
// which pipeline steps filtered out which hosts.
func generateExplanation(result *v1alpha1.DecisionResult, pipelineErr error) string {
	if pipelineErr != nil {
		return fmt.Sprintf("Pipeline run failed: %s.", pipelineErr.Error())
	}

	if result == nil || len(result.StepResults) == 0 {
		if result != nil && result.TargetHost != nil {
			return fmt.Sprintf("Selected host: %s.", *result.TargetHost)
		}
		return ""
	}

	// Get all initial hosts from input weights.
	var allHosts map[string]float64
	if len(result.RawInWeights) > 0 {
		allHosts = result.RawInWeights
	} else if len(result.NormalizedInWeights) > 0 {
		allHosts = result.NormalizedInWeights
	}
	if allHosts == nil {
		if result.TargetHost != nil {
			return fmt.Sprintf("Selected host: %s.", *result.TargetHost)
		}
		return ""
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Started with %d host(s).\n\n", len(allHosts))

	// Track current set of surviving hosts.
	currentHosts := make(map[string]bool, len(allHosts))
	for h := range allHosts {
		currentHosts[h] = true
	}

	for _, step := range result.StepResults {
		// Determine which hosts were removed by this step.
		var removed []string
		for h := range currentHosts {
			if _, exists := step.Activations[h]; !exists {
				removed = append(removed, h)
			}
		}
		if len(removed) > 0 {
			sort.Strings(removed)
			for _, h := range removed {
				delete(currentHosts, h)
			}
			fmt.Fprintf(&sb, "%s filtered out %s\n",
				step.StepName,
				joinHostsCapped(removed, maxHostsInExplanation),
			)
		}
	}

	// Summary of remaining hosts.
	remaining := make([]string, 0, len(currentHosts))
	for h := range currentHosts {
		remaining = append(remaining, h)
	}
	sort.Strings(remaining)
	fmt.Fprintf(&sb, "\n%d hosts remaining (%s)\n",
		len(remaining),
		joinHostsCapped(remaining, maxHostsInExplanation),
	)

	if result.TargetHost != nil {
		fmt.Fprintf(&sb, "\nSelected host: %s.", *result.TargetHost)
	}

	return strings.TrimSpace(sb.String())
}

// HistoryClient manages History CRDs for scheduling decisions. It holds the
// Kubernetes client and event recorder so callers don't have to pass them on
// every invocation.
type HistoryClient struct {
	Client   client.Client
	Recorder events.EventRecorder
}

// CreateOrUpdateHistory creates or updates a History CRD for the given decision.
// It is called after every pipeline run (success and failure). The pipelineErr
// parameter is used to generate a meaningful explanation when the pipeline fails.
// If a non-nil Recorder is set, a Kubernetes Event is emitted on the History
// object to short-term persist the scheduling decision.
func (h *HistoryClient) CreateOrUpdateHistory(
	ctx context.Context,
	decision *v1alpha1.Decision,
	az *string,
	pipelineErr error,
) error {

	if decision == nil {
		return errors.New("decision cannot be nil")
	}

	log := ctrl.LoggerFrom(ctx)

	name := getName(decision.Spec.SchedulingDomain, decision.Spec.ResourceID)

	history := &v1alpha1.History{}
	err := h.Client.Get(ctx, client.ObjectKey{Name: name}, history)

	if apierrors.IsNotFound(err) {
		// Create new History CRD.
		history = &v1alpha1.History{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
			Spec: v1alpha1.HistorySpec{
				SchedulingDomain: decision.Spec.SchedulingDomain,
				ResourceID:       decision.Spec.ResourceID,
				AvailabilityZone: az,
			},
		}
		if createErr := h.Client.Create(ctx, history); createErr != nil {
			if apierrors.IsAlreadyExists(createErr) {
				// Another controller beat us to it, re-fetch.
				if getErr := h.Client.Get(ctx, client.ObjectKey{Name: name}, history); getErr != nil {
					return getErr
				}
			} else {
				log.Error(createErr, "failed to create history CRD", "name", name)
				return createErr
			}
		}
	} else if err != nil {
		log.Error(err, "failed to get history CRD", "name", name)
		return err
	}

	successful := pipelineErr == nil && decision.Status.Result != nil && decision.Status.Result.TargetHost != nil

	namespacedName := client.ObjectKey{Name: name}

	// Use Update instead of MergeFrom+Patch because JSON merge patch strips
	// boolean false values, which causes CRD validation to reject the patch
	// when Successful is false. Retry on conflict to handle concurrent updates.
	firstAttempt := true
	if retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// On retries, re-fetch the latest History object after a conflict.
		if !firstAttempt {
			if getErr := h.Client.Get(ctx, namespacedName, history); getErr != nil {
				return getErr
			}
		}
		firstAttempt = false

		// Archive the previous current decision into the history list.
		if !history.Status.Current.Timestamp.IsZero() {
			orderedHosts := history.Status.Current.OrderedHosts
			if orderedHosts == nil {
				orderedHosts = []string{}
			}
			entry := v1alpha1.SchedulingHistoryEntry{
				Timestamp:    history.Status.Current.Timestamp,
				PipelineRef:  history.Status.Current.PipelineRef,
				Intent:       history.Status.Current.Intent,
				OrderedHosts: orderedHosts,
				Successful:   history.Status.Current.Successful,
			}
			history.Status.History = append(history.Status.History, entry)
			if len(history.Status.History) > maxHistoryEntries {
				history.Status.History = history.Status.History[len(history.Status.History)-maxHistoryEntries:]
			}
		}

		// Build the new current decision.
		current := v1alpha1.CurrentDecision{
			Timestamp:   metav1.Now(),
			PipelineRef: decision.Spec.PipelineRef,
			Intent:      decision.Spec.Intent,
			Successful:  successful,
			Explanation: generateExplanation(decision.Status.Result, pipelineErr),
		}

		current.OrderedHosts = []string{}
		if decision.Status.Result != nil {
			current.TargetHost = decision.Status.Result.TargetHost
			hosts := decision.Status.Result.OrderedHosts
			if len(hosts) > maxHostsInOrderedList {
				hosts = hosts[:maxHostsInOrderedList]
			}
			current.OrderedHosts = hosts
		}
		history.Status.Current = current

		// Set Ready condition — True only when a host was successfully selected.
		condStatus := metav1.ConditionTrue
		reason := v1alpha1.HistoryReasonSchedulingSucceeded
		message := "scheduling decision selected a target host"
		if pipelineErr != nil {
			condStatus = metav1.ConditionFalse
			reason = v1alpha1.HistoryReasonPipelineRunFailed
			message = "pipeline run failed: " + pipelineErr.Error()
		} else if !successful {
			condStatus = metav1.ConditionFalse
			reason = v1alpha1.HistoryReasonNoHostFound
			message = "pipeline completed but no suitable host was found"
		}
		meta.SetStatusCondition(&history.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.HistoryConditionReady,
			Status:  condStatus,
			Reason:  reason,
			Message: message,
		})

		return h.Client.Status().Update(ctx, history)
	}); retryErr != nil {
		log.Error(retryErr, "failed to update history CRD status", "name", name)
		return retryErr
	}

	// Emit a Kubernetes Event on the History object to short-term persist the
	// scheduling decision. Events auto-expire (default TTL ~1h) so this gives
	// devops short-term visibility into individual scheduling runs.
	if h.Recorder != nil {
		eventType := corev1.EventTypeNormal
		eventReason := v1alpha1.HistoryReasonSchedulingSucceeded
		action := "Scheduled"
		if !successful {
			eventType = corev1.EventTypeWarning
			eventReason = "SchedulingFailed"
			action = "FailedScheduling"
		}
		h.Recorder.Eventf(history, nil, eventType, eventReason, action, "%s", history.Status.Current.Explanation)
	}

	log.Info("history CRD updated", "name", name, "entries", len(history.Status.History))
	return nil
}

// Delete deletes the History CRD associated with the given scheduling domain
// and resource ID. It is a no-op if the History CRD does not exist.
func (h *HistoryClient) Delete(
	ctx context.Context,
	schedulingDomain v1alpha1.SchedulingDomain,
	resourceID string,
) error {

	log := ctrl.LoggerFrom(ctx)
	name := getName(schedulingDomain, resourceID)

	history := &v1alpha1.History{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	if err := h.Client.Delete(ctx, history); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		log.Error(err, "failed to delete history CRD", "name", name)
		return err
	}
	log.Info("deleted history CRD", "name", name)
	return nil
}
