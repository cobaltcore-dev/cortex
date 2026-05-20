// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
)

// ComputeStatusSummary produces a compact human-readable summary of the committed resource's
// current state for the kubectl wide view.
//
// Format: {reason}[( diff)] [· {N} VM[s]] [· exp in {duration}|no expiry]
func ComputeStatusSummary(spec CommittedResourceSpec, status CommittedResourceStatus, now time.Time) string {
	cond := meta.FindStatusCondition(status.Conditions, CommittedResourceConditionReady)
	if cond == nil {
		return ""
	}
	reason := cond.Reason

	var parts []string

	// Reason with optional spec-vs-acceptedSpec diff.
	if diff := buildSpecDiff(spec, status.AcceptedSpec); diff != "" {
		parts = append(parts, fmt.Sprintf("%s (%s)", reason, diff))
	} else {
		parts = append(parts, reason)
	}

	// VM count — only meaningful once placement is accepted.
	if reason == CommittedResourceReasonAccepted {
		n := len(status.AssignedInstances)
		if n == 1 {
			parts = append(parts, "1 VM")
		} else {
			parts = append(parts, fmt.Sprintf("%d VMs", n))
		}
	}

	// Expiry — omit for Rejected (CR is terminal, expiry irrelevant).
	if reason != CommittedResourceReasonRejected {
		if spec.EndTime == nil {
			parts = append(parts, "no expiry")
		} else if remaining := spec.EndTime.Sub(now); remaining <= 0 {
			parts = append(parts, "expired")
		} else {
			parts = append(parts, "exp in "+formatRemaining(remaining))
		}
	}

	return strings.Join(parts, " · ")
}

// buildSpecDiff returns a semicolon-separated list of placement-relevant field changes
// between spec and the last accepted spec.
func buildSpecDiff(spec CommittedResourceSpec, accepted *CommittedResourceSpec) string {
	if accepted == nil {
		return ""
	}
	var diffs []string
	if spec.Amount.Cmp(accepted.Amount) != 0 {
		diffs = append(diffs, fmt.Sprintf("amount %s→%s", accepted.Amount.String(), spec.Amount.String()))
	}
	if spec.FlavorGroupName != accepted.FlavorGroupName {
		diffs = append(diffs, fmt.Sprintf("fg %s→%s", accepted.FlavorGroupName, spec.FlavorGroupName))
	}
	if spec.AvailabilityZone != accepted.AvailabilityZone {
		diffs = append(diffs, fmt.Sprintf("az %s→%s", accepted.AvailabilityZone, spec.AvailabilityZone))
	}
	if spec.ResourceType != accepted.ResourceType {
		diffs = append(diffs, fmt.Sprintf("type %s→%s", accepted.ResourceType, spec.ResourceType))
	}
	return strings.Join(diffs, "; ")
}

// formatRemaining formats a positive duration as a compact two-unit string,
// scaling from days down to seconds based on magnitude.
func formatRemaining(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d.Minutes())
		s := int(d.Seconds()) - m*60
		return fmt.Sprintf("%dm %ds", m, s)
	}
	if d < 24*time.Hour {
		h := int(d.Hours())
		m := int(d.Minutes()) - h*60
		return fmt.Sprintf("%dh %dm", h, m)
	}
	days := int(d.Hours()) / 24
	h := int(d.Hours()) - days*24
	return fmt.Sprintf("%dd %dh", days, h)
}
