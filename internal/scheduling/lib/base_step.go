// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"errors"
	"fmt"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Common base for all steps that provides some functionality
// that would otherwise be duplicated across all steps.
type BaseStep[RequestType PipelineRequest, Opts StepOpts] struct {
	// Options to pass via yaml to this step.
	conf.JsonOpts[Opts]
	// The activation function to use.
	ActivationFunction
	// The kubernetes client to use.
	Client client.Client
}

// Init the step with the database and options.
func (s *BaseStep[RequestType, Opts]) Init(ctx context.Context, client client.Client, step v1alpha1.StepSpec) error {
	opts := conf.NewRawOptsBytes(step.Params.Raw)
	if err := s.Load(opts); err != nil {
		return err
	}
	if err := s.Options.Validate(); err != nil {
		return err
	}

	s.Client = client
	return nil
}

// Check if all knowledges are ready, and if not, return an error indicating why not.
func (d *BaseStep[RequestType, Opts]) CheckKnowledges(ctx context.Context, kns ...corev1.ObjectReference) error {
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

// Get a default result (no action) for the input weight keys given in the request.
// Use this to initialize the result before applying filtering/weighing logic.
func (s *BaseStep[RequestType, Opts]) IncludeAllHostsFromRequest(request RequestType) *StepResult {
	activations := make(map[string]float64)
	for _, subject := range request.GetSubjects() {
		activations[subject] = s.NoEffect()
	}
	stats := make(map[string]StepStatistics)
	return &StepResult{Activations: activations, Statistics: stats}
}

// Get default statistics for the input weight keys given in the request.
func (s *BaseStep[RequestType, Opts]) PrepareStats(request RequestType, unit string) StepStatistics {
	return StepStatistics{
		Unit:     unit,
		Subjects: make(map[string]float64, len(request.GetSubjects())),
	}
}
