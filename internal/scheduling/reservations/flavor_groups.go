// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import (
	"context"
	"errors"
	"fmt"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// FlavorGroupKnowledge accesses flavor group data from Knowledge CRDs.
type FlavorGroupKnowledge struct {
	client.Client
}

func (k *FlavorGroupKnowledge) IsReady(ctx context.Context) (*v1alpha1.Knowledge, error) {
	// List all Knowledge CRDs
	var knowledgeList v1alpha1.KnowledgeList
	if err := k.List(ctx, &knowledgeList); err != nil {
		return nil, fmt.Errorf("failed to list knowledge CRDs: %w", err)
	}

	// Find the flavor groups knowledge CRD
	var flavorGroupsKnowledge *v1alpha1.Knowledge
	for _, knowledge := range knowledgeList.Items {
		if knowledge.Spec.SchedulingDomain == v1alpha1.SchedulingDomainNova &&
			knowledge.Spec.Extractor.Name == "flavor_groups" {
			flavorGroupsKnowledge = &knowledge
			break
		}
	}

	if flavorGroupsKnowledge == nil {
		return nil, errors.New("flavor groups knowledge CRD not found")
	}

	// Check if knowledge is ready
	for _, condition := range flavorGroupsKnowledge.Status.Conditions {
		if condition.Type == v1alpha1.KnowledgeConditionReady && condition.Status == "True" {
			return flavorGroupsKnowledge, nil
		}
	}

	// Not ready yet
	return nil, nil
}

// GetVersion returns Unix timestamp of last content change, or -1 if not ready.
func (k *FlavorGroupKnowledge) GetVersion(ctx context.Context) int64 {
	knowledgeCRD, err := k.IsReady(ctx)
	if err != nil || knowledgeCRD == nil {
		return -1
	}
	// Return Unix timestamp as version
	// If LastContentChange is zero (never set), return -1
	if knowledgeCRD.Status.LastContentChange.IsZero() {
		return -1
	}
	return knowledgeCRD.Status.LastContentChange.Unix()
}

func (k *FlavorGroupKnowledge) GetAllFlavorGroups(ctx context.Context, knowledgeCRD *v1alpha1.Knowledge) (map[string]compute.FlavorGroupFeature, error) {
	// If no CRD provided, fetch it
	if knowledgeCRD == nil {
		var err error
		knowledgeCRD, err = k.IsReady(ctx)
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
