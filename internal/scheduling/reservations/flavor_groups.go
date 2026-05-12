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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var flavorGroupsLog = ctrl.Log.WithName("flavor_groups")

// FindFlavorInGroups searches all flavor groups for a flavor by name.
// Returns the flavor group name and flavor details, or an error if the flavor
// is not found in any group.
func FindFlavorInGroups(flavorName string, flavorGroups map[string]compute.FlavorGroupFeature) (groupName string, flavor *compute.FlavorInGroup, err error) {
	for gName, fg := range flavorGroups {
		for i, f := range fg.Flavors {
			if f.Name == flavorName {
				return gName, &fg.Flavors[i], nil
			}
		}
	}
	return "", nil, fmt.Errorf("flavor %q not found in any flavor group", flavorName)
}

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

	// Build map for efficient lookups, skipping any groups that fail validation.
	flavorGroupMap := make(map[string]compute.FlavorGroupFeature, len(features))
	for _, feature := range features {
		if err := feature.Validate(); err != nil {
			flavorGroupsLog.Error(err, "skipping invalid flavor group from Knowledge CRD")
			continue
		}
		flavorGroupMap[feature.Name] = feature
	}

	return flavorGroupMap, nil
}
