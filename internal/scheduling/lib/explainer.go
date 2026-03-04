// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"

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
	return "Explanation generation not implemented yet", nil
}
