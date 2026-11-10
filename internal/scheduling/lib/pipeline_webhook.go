// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"fmt"

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

// validatePipeline performs version-specific validation of the pipeline steps.
//
// It never returns an error: structural constraints (pipeline type, which step
// lists are allowed) are enforced by the API server via CEL on the CRD, so the
// webhook only checks whether the referenced steps exist in this controller and
// whether their params are valid for this controller version. Those checks are
// version-specific, so failures are surfaced as warnings rather than rejections
// to avoid blocking rolling upgrades where an older controller sees a pipeline
// written for a newer one (or vice versa).
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

	log.V(1).Info("validating pipeline", "pipelineName", pipeline.Name, "pipelineType", pipeline.Spec.Type)

	var warnings []string
	validate := func(kind, name string, index map[string]Validatable, params v1alpha1.Parameters) {
		step, ok := index[name]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("unknown %s %q: this step will be ignored", kind, name))
			return
		}
		if err := step.Validate(ctx, params); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s %q: %v", kind, name, err))
		}
	}
	for _, filterSpec := range pipeline.Spec.Filters {
		validate("filter", filterSpec.Name, w.ValidatableFilters, filterSpec.Params)
	}
	for _, weigherSpec := range pipeline.Spec.Weighers {
		validate("weigher", weigherSpec.Name, w.ValidatableWeighers, weigherSpec.Params)
	}
	for _, detectorSpec := range pipeline.Spec.Detectors {
		validate("detector", detectorSpec.Name, w.ValidatableDetectors, detectorSpec.Params)
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
