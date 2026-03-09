// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The explainer gets a scheduling decision and produces a human-readable
// explanation of why the decision was made the way it was.
type Explainer struct {
	// The kubernetes client to use for fetching related data.
	client.Client
}

// NewExplainer creates a new explainer with template support.
func NewExplainer(client client.Client) (*Explainer, error) {
	return &Explainer{
		Client: client,
	}, nil
}

// Explain the given decision and return a human-readable explanation.
func (e *Explainer) Explain(ctx context.Context, decision DecisionUpdate) (string, error) {
	result := decision.Result
	var b strings.Builder

	// Collect all input hosts sorted for deterministic output.
	allHosts := make([]string, 0, len(result.RawInWeights))
	for host := range result.RawInWeights {
		allHosts = append(allHosts, host)
	}
	sort.Strings(allHosts)

	fmt.Fprintf(&b, "Pipeline %q evaluated %d host(s): [%s].\n",
		decision.PipelineName, len(allHosts), strings.Join(allHosts, ", "))

	// Walk through each step result and explain what happened.
	// For filter steps: hosts absent from Activations were filtered out.
	currentHosts := make(map[string]bool, len(allHosts))
	for _, h := range allHosts {
		currentHosts[h] = true
	}

	for _, step := range result.StepResults {
		// Determine which of the current hosts are missing from this step's activations.
		var removed []string
		for host := range currentHosts {
			if _, ok := step.Activations[host]; !ok {
				removed = append(removed, host)
			}
		}
		sort.Strings(removed)

		if len(removed) > 0 {
			// This is a filter step that removed hosts.
			for _, h := range removed {
				delete(currentHosts, h)
			}
			fmt.Fprintf(&b, "Step %q filtered out: [%s]. %d host(s) remaining.\n",
				step.StepName, strings.Join(removed, ", "), len(currentHosts))
		}
	}

	// Determine surviving hosts from AggregatedOutWeights.
	var surviving []string
	for _, host := range allHosts {
		if _, ok := result.AggregatedOutWeights[host]; ok {
			surviving = append(surviving, host)
		}
	}

	if len(surviving) == 0 {
		b.WriteString("No hosts remaining after filtering. Scheduling failed.")
		return b.String(), nil
	}

	fmt.Fprintf(&b, "Remaining %d host(s) after all filters: [%s].\n",
		len(surviving), strings.Join(surviving, ", "))

	// Show final ranking from OrderedHosts.
	if len(result.OrderedHosts) > 0 {
		fmt.Fprintf(&b, "Final ranking: %s.\n", strings.Join(result.OrderedHosts, " > "))
		fmt.Fprintf(&b, "Selected target: %s.", result.OrderedHosts[0])
	}

	return b.String(), nil
}
