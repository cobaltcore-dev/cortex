// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

// maxHostsInExplanation caps how many host names are listed per line in a
// generated explanation before the remainder is summarized as a count.
const maxHostsInExplanation = 10

// joinHostsCapped joins up to max host names. If hosts exceeds max, it appends
// a count of the omitted entries, e.g. "host-a, host-b (and 48 more)".
func joinHostsCapped(hosts []string, maxHosts int) string {
	if len(hosts) <= maxHosts {
		return strings.Join(hosts, ", ")
	}
	return fmt.Sprintf("%s (and %d more)", strings.Join(hosts[:maxHosts], ", "), len(hosts)-maxHosts)
}

// GenerateExplanation produces a human-readable explanation from a decision
// result. On failure it includes the error. On success it describes which
// pipeline steps filtered out which hosts and, for the surviving hosts, how the
// weighers ranked them (via ExplainWeighing). The result is written to
// Decision.Status.Explanation so it propagates to the scheduled resource (e.g.
// the VM CortexSpec.Explanation).
func GenerateExplanation(result *v1alpha1.DecisionResult, pipelineErr error) string {
	if pipelineErr != nil {
		return fmt.Sprintf("Pipeline run failed: %s.", pipelineErr.Error())
	}

	if result == nil {
		return "No decision result recorded."
	}
	if len(result.StepResults) == 0 {
		if result.TargetHost != nil {
			return fmt.Sprintf("Selected host: %s.", *result.TargetHost)
		}
		return "No pipeline steps recorded."
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
		return "No host weight data recorded."
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

	if weighingExpl := ExplainWeighing(result); weighingExpl != "" {
		fmt.Fprintf(&sb, "\n\n%s", weighingExpl)
	}

	return strings.TrimSpace(sb.String())
}
