// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"fmt"
	"strings"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// Validatable is implemented by all pipeline steps (filters, weighers, detectors).
// It allows validation of step parameters without full initialization.
type Validatable interface {
	// Validate checks if the given parameters are valid for this step.
	Validate(ctx context.Context, params v1alpha1.Parameters) error
}

// PipelineAdmissionWebhook validates Pipeline resources for a specific scheduling domain.
// It checks that all configured steps (filters, weighers, detectors) exist in the
// provided indexes and that their parameters are valid.
type PipelineAdmissionWebhook struct {
	// The scheduling domain this webhook handles (e.g., nova, cinder, manila).
	SchedulingDomain v1alpha1.SchedulingDomain
	// ValidatableFilters maps filter names to validatable filter instances.
	ValidatableFilters map[string]Validatable
	// ValidatableWeighers maps weigher names to validatable weigher instances.
	ValidatableWeighers map[string]Validatable
	// ValidatableDetectors maps detector names to validatable detector instances.
	ValidatableDetectors map[string]Validatable
}

// ValidateCreate implements admission.Validator.
func (w *PipelineAdmissionWebhook) ValidateCreate(
	ctx context.Context,
	pipeline *v1alpha1.Pipeline,
) (admission.Warnings, error) {

	return w.validatePipeline(ctx, pipeline)
}

// ValidateUpdate implements admission.Validator.
func (w *PipelineAdmissionWebhook) ValidateUpdate(
	ctx context.Context,
	oldPipeline, newPipeline *v1alpha1.Pipeline,
) (admission.Warnings, error) {

	return w.validatePipeline(ctx, newPipeline)
}

// ValidateDelete implements admission.Validator.
func (w *PipelineAdmissionWebhook) ValidateDelete(
	ctx context.Context,
	pipeline *v1alpha1.Pipeline,
) (admission.Warnings, error) {

	return nil, nil // No validation needed on delete.
}

// validatePipeline performs the actual validation logic.
func (w *PipelineAdmissionWebhook) validatePipeline(
	ctx context.Context,
	pipeline *v1alpha1.Pipeline,
) (admission.Warnings, error) {

	log := ctrl.Log.WithName("pipeline-webhook")

	// Check if this pipeline's scheduling domain matches our webhook's domain.
	// If not, skip validation and allow the request - another webhook should handle it.
	if pipeline.Spec.SchedulingDomain != w.SchedulingDomain {
		log.V(1).Info("skipping validation for pipeline with different scheduling domain",
			"pipeline", pipeline.Name,
			"pipelineDomain", pipeline.Spec.SchedulingDomain,
			"webhookDomain", w.SchedulingDomain)
		return nil, nil
	}

	log.Info("validating pipeline", "pipelineName", pipeline.Name, "pipelineType", pipeline.Spec.Type)

	var errMsgs []string

	// We will use warnings whenever a pipeline step is referenced that
	// doesn't exist in our indexes, in case cortex is updated with new
	// filters/weighers/detectors, so we don't break our rollout.
	var warnings []string

	// Validate based on pipeline type
	switch pipeline.Spec.Type {
	case v1alpha1.PipelineTypeFilterWeigher:
		// Check there are no detectors configured,
		// as they are not allowed in a filter/weigher pipeline.
		if len(pipeline.Spec.Detectors) > 0 {
			errMsgs = append(errMsgs, "detectors are not allowed in a filter/weigher pipeline")
		}
		for _, filterSpec := range pipeline.Spec.Filters {
			filter, ok := w.ValidatableFilters[filterSpec.Name]
			if !ok {
				warnings = append(warnings, fmt.Sprintf("unknown filter %q: this filter will be ignored", filterSpec.Name))
				continue
			}
			if err := filter.Validate(ctx, filterSpec.Params); err != nil {
				errMsgs = append(errMsgs, fmt.Sprintf("filter %q: %v", filterSpec.Name, err))
			}
		}
		for _, weigherSpec := range pipeline.Spec.Weighers {
			weigher, ok := w.ValidatableWeighers[weigherSpec.Name]
			if !ok {
				warnings = append(warnings, fmt.Sprintf("unknown weigher %q: this weigher will be ignored", weigherSpec.Name))
				continue
			}
			if err := weigher.Validate(ctx, weigherSpec.Params); err != nil {
				errMsgs = append(errMsgs, fmt.Sprintf("weigher %q: %v", weigherSpec.Name, err))
			}
		}
	case v1alpha1.PipelineTypeDetector:
		// Check there are no filters or weighers configured,
		// as they are not allowed in a detector pipeline.
		if len(pipeline.Spec.Filters) > 0 {
			errMsgs = append(errMsgs, "filters are not allowed in a detector pipeline")
		}
		if len(pipeline.Spec.Weighers) > 0 {
			errMsgs = append(errMsgs, "weighers are not allowed in a detector pipeline")
		}
		for _, detectorSpec := range pipeline.Spec.Detectors {
			detector, ok := w.ValidatableDetectors[detectorSpec.Name]
			if !ok {
				warnings = append(warnings, fmt.Sprintf("unknown detector %q: this detector will be ignored", detectorSpec.Name))
				continue
			}
			if err := detector.Validate(ctx, detectorSpec.Params); err != nil {
				errMsgs = append(errMsgs, fmt.Sprintf("detector %q: %v", detectorSpec.Name, err))
			}
		}
	default:
		errMsgs = append(errMsgs, fmt.Sprintf("unknown pipeline type: %s", pipeline.Spec.Type))
	}

	if len(errMsgs) > 0 {
		return warnings, fmt.Errorf("pipeline is invalid: %s", strings.Join(errMsgs, "; "))
	}
	return warnings, nil
}

// SetupWebhookWithManager sets up the validating webhook for Pipeline resources.
func (w *PipelineAdmissionWebhook) SetupWebhookWithManager(mgr ctrl.Manager) error {
	log := ctrl.Log.WithName("pipeline-webhook-setup")
	log.Info("setting up validating webhook for pipelines",
		"schedulingDomain", w.SchedulingDomain)
	return ctrl.NewWebhookManagedBy(mgr, &v1alpha1.Pipeline{}).
		WithValidator(w).
		Complete()
}
