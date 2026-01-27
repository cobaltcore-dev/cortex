// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"errors"
	"fmt"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Interface for a weigher as part of the scheduling pipeline.
type Weigher[RequestType FilterWeigherPipelineRequest] interface {
	FilterWeigherPipelineStep[RequestType]

	// Configure the step and initialize things like a database connection.
	Init(ctx context.Context, client client.Client, step v1alpha1.WeigherSpec) error
}

// Common base for all steps that provides some functionality
// that would otherwise be duplicated across all steps.
type BaseWeigher[RequestType FilterWeigherPipelineRequest, Opts FilterWeigherPipelineStepOpts] struct {
	BaseFilterWeigherPipelineStep[RequestType, Opts]
}

// Init the weigher with the database and options.
func (s *BaseWeigher[RequestType, Opts]) Init(ctx context.Context, client client.Client, step v1alpha1.WeigherSpec) error {
	return s.BaseFilterWeigherPipelineStep.Init(ctx, client, step.Params)
}

// Check if all knowledges are ready, and if not, return an error indicating why not.
func (d *BaseFilterWeigherPipelineStep[RequestType, Opts]) CheckKnowledges(ctx context.Context, kns ...corev1.ObjectReference) error {
	if d.Client == nil {
		return errors.New("kubernetes client not initialized")
	}
	for _, objRef := range kns {
		knowledge := &v1alpha1.Knowledge{}
		if err := d.Client.Get(ctx, client.ObjectKey{
			Name:      objRef.Name,
			Namespace: objRef.Namespace,
		}, knowledge); err != nil {
			return fmt.Errorf("failed to get knowledge %s: %w", objRef.Name, err)
		}
		// Check if the knowledge status conditions indicate an error.
		if meta.IsStatusConditionFalse(knowledge.Status.Conditions, v1alpha1.KnowledgeConditionReady) {
			return fmt.Errorf("knowledge %s not ready", objRef.Name)
		}
		if knowledge.Status.RawLength == 0 {
			return fmt.Errorf("knowledge %s not ready, no data available", objRef.Name)
		}
	}
	return nil
}
