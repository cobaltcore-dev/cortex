// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import (
	"context"
	"errors"
	"fmt"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// FlavorGroupKnowledgeClient accesses flavor group data from Knowledge CRDs.
type FlavorGroupKnowledgeClient struct {
	client.Client
}

// Get retrieves the flavor groups Knowledge CRD and returns it if ready.
// Returns nil, nil if not ready yet.
func (c *FlavorGroupKnowledgeClient) Get(ctx context.Context) (*v1alpha1.Knowledge, error) {
	knowledge := &v1alpha1.Knowledge{}
	err := c.Client.Get(ctx, types.NamespacedName{
		Name: "flavor-groups",
		// Namespace is empty as Knowledge is cluster-scoped
	}, knowledge)

	if err != nil {
		return nil, fmt.Errorf("failed to get flavor groups knowledge: %w", err)
	}

	if meta.IsStatusConditionTrue(knowledge.Status.Conditions, v1alpha1.KnowledgeConditionReady) {
		return knowledge, nil
	}

	// Found but not ready yet
	return nil, nil
}

// GetAllFlavorGroups returns all flavor groups as a map.
// If knowledgeCRD is provided, uses it directly. Otherwise fetches the Knowledge CRD.
func (c *FlavorGroupKnowledgeClient) GetAllFlavorGroups(ctx context.Context, knowledgeCRD *v1alpha1.Knowledge) (map[string]compute.FlavorGroupFeature, error) {
	// If no CRD provided, fetch it
	if knowledgeCRD == nil {
		var err error
		knowledgeCRD, err = c.Get(ctx)
		if err != nil {
			return nil, err
		}
		if knowledgeCRD == nil {
			return nil, errors.New("flavor groups knowledge is not ready")
		}
	}

	// Unbox the features from the raw extension
	features, err := v1alpha1.UnboxFeatureList[compute.FlavorGroupFeature](
		knowledgeCRD.Status.Raw,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to unbox flavor group features: %w", err)
	}

	// Build map for efficient lookups
	flavorGroupMap := make(map[string]compute.FlavorGroupFeature, len(features))
	for _, feature := range features {
		flavorGroupMap[feature.Name] = feature
	}

	return flavorGroupMap, nil
}
