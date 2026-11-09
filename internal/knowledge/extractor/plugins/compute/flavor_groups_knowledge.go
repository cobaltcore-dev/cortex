// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"context"
	"errors"
	"fmt"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetFlavorGroupsKnowledge retrieves the flavor groups Knowledge CRD and returns
// it if ready. Returns nil, nil if the CRD exists but is not ready yet.
func GetFlavorGroupsKnowledge(ctx context.Context, c client.Client) (*v1alpha1.Knowledge, error) {
	knowledge := &v1alpha1.Knowledge{}
	// Namespace is empty as Knowledge is cluster-scoped.
	if err := c.Get(ctx, types.NamespacedName{Name: "flavor-groups"}, knowledge); err != nil {
		return nil, fmt.Errorf("failed to get flavor groups knowledge: %w", err)
	}
	if meta.IsStatusConditionTrue(knowledge.Status.Conditions, v1alpha1.KnowledgeConditionReady) {
		return knowledge, nil
	}
	return nil, nil
}

// GetAllFlavorGroups returns all flavor groups as a map keyed by group name,
// skipping any that fail validation. If knowledgeCRD is nil the Knowledge CRD is
// fetched first (erroring if it is not ready); otherwise the provided CRD is used.
func GetAllFlavorGroups(ctx context.Context, c client.Client, knowledgeCRD *v1alpha1.Knowledge) (map[string]FlavorGroupFeature, error) {
	if knowledgeCRD == nil {
		var err error
		knowledgeCRD, err = GetFlavorGroupsKnowledge(ctx, c)
		if err != nil {
			return nil, err
		}
		if knowledgeCRD == nil {
			return nil, errors.New("flavor groups knowledge is not ready")
		}
	}
	features, err := v1alpha1.UnboxFeatureList[FlavorGroupFeature](knowledgeCRD.Status.Raw)
	if err != nil {
		return nil, fmt.Errorf("failed to unbox flavor group features: %w", err)
	}
	flavorGroupMap := make(map[string]FlavorGroupFeature, len(features))
	for _, feature := range features {
		if err := feature.Validate(); err != nil {
			flavorGroupLog.Error(err, "skipping invalid flavor group from Knowledge CRD")
			continue
		}
		flavorGroupMap[feature.Name] = feature
	}
	return flavorGroupMap, nil
}

// FindFlavorInGroups searches all flavor groups for a flavor by name, returning
// the flavor group name and flavor details, or an error if the flavor is not
// found in any group.
func FindFlavorInGroups(flavorName string, flavorGroups map[string]FlavorGroupFeature) (groupName string, flavor *FlavorInGroup, err error) {
	for gName, fg := range flavorGroups {
		for i, f := range fg.Flavors {
			if f.Name == flavorName {
				return gName, &fg.Flavors[i], nil
			}
		}
	}
	return "", nil, fmt.Errorf("flavor %q not found in any flavor group", flavorName)
}
