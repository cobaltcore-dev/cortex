// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package explanation

import (
	"context"
	"fmt"
	"strings"

	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The explainer gets a scheduling decision and produces a human-readable
// explanation of why the decision was made the way it was.
type Explainer struct {
	// The kubernetes client to use for fetching related data.
	client.Client
}

// Explain the given decision and return a human-readable explanation.
func (e *Explainer) Explain(ctx context.Context, decision *v1alpha1.Decision) (string, error) {
	expl := []string{}

	resourceType := ""
	switch decision.Spec.Type {
	case v1alpha1.DecisionTypeNovaServer:
		resourceType = "nova server"
	case v1alpha1.DecisionTypeManilaShare:
		resourceType = "manila share"
	case v1alpha1.DecisionTypeCinderVolume:
		resourceType = "cinder volume"
	case v1alpha1.DecisionTypeIroncoreMachine:
		resourceType = "ironcore machine"
	default:
		resourceType = "resource"
	}

	// Check the history of this decision.
	history := decision.Status.History
	if history == nil || len(*history) == 0 {
		expl = append(expl, "Initial placement of the "+resourceType+".\n")
	} else {
		// Get the last decision.
		lastDecisionRef := (*history)[len(*history)-1]
		lastDecision := &v1alpha1.Decision{}
		if err := e.Get(ctx, client.ObjectKey{
			Namespace: lastDecisionRef.Namespace,
			Name:      lastDecisionRef.Name,
		}, lastDecision); err != nil {
			return "", err
		}
		lastTarget := "(n/a)"
		if lastDecision.Status.Result != nil {
			if lastDecision.Status.Result.TargetHost != nil {
				lastTarget = *lastDecision.Status.Result.TargetHost
			}
		}
		newTarget := "(n/a)"
		if decision.Status.Result != nil {
			if decision.Status.Result.TargetHost != nil {
				newTarget = *decision.Status.Result.TargetHost
			}
		}
		expl = append(expl, fmt.Sprintf(
			"Decision #%d for this %s. Previous target host was '%s', now it's '%s'",
			len(*history)+1, resourceType, lastTarget, newTarget,
		))
	}

	// TODO: Add more detailed explanation based on decision context.
	// You can also fetch pipeline + step explanations here to enrich the output.
	// Just use the refs included in the decision status/spec.

	return strings.Join(expl, "\n"), nil
}
