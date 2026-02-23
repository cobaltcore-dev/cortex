// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"fmt"
	"strings"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// Validatable is implemented by all pipeline steps (filters, weighers, detectors).
// It allows validation of step parameters without full initialization.
type Validatable interface {
	// Validate checks if the given parameters are valid for this step.
	Validate(ctx context.Context, params runtime.RawExtension) error
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

	// Validate based on pipeline type
	switch pipeline.Spec.Type {
	case v1alpha1.PipelineTypeFilterWeigher:
		// Check there are no detectors configured,
		// as they are not allowed in a filter/weigher pipeline.
		if len(pipeline.Spec.Detectors) > 0 {
			errMsgs = append(errMsgs, "detectors are not allowed in a filter/weigher pipeline")
		}
		for _, filterSpec := range pipeline.Spec.Filters {
			if err := w.validateFilter(ctx, filterSpec); err != nil {
				errMsgs = append(errMsgs, fmt.Sprintf("filter %q: %v", filterSpec.Name, err))
			}
		}
		for _, weigherSpec := range pipeline.Spec.Weighers {
			if err := w.validateWeigher(ctx, weigherSpec); err != nil {
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
			if err := w.validateDetector(ctx, detectorSpec); err != nil {
				errMsgs = append(errMsgs, fmt.Sprintf("detector %q: %v", detectorSpec.Name, err))
			}
		}
	default:
		errMsgs = append(errMsgs, fmt.Sprintf("unknown pipeline type: %s", pipeline.Spec.Type))
	}

	if len(errMsgs) > 0 {
		return nil, fmt.Errorf("pipeline is invalid: %s", strings.Join(errMsgs, "; "))
	}

	return nil, nil
}

// validateFilter validates a single filter specification.
func (w *PipelineAdmissionWebhook) validateFilter(ctx context.Context, spec v1alpha1.FilterSpec) error {
	filter, ok := w.ValidatableFilters[spec.Name]
	if !ok {
		return fmt.Errorf("unknown filter: %q", spec.Name)
	}
	return filter.Validate(ctx, spec.Params)
}

// validateWeigher validates a single weigher specification.
func (w *PipelineAdmissionWebhook) validateWeigher(ctx context.Context, spec v1alpha1.WeigherSpec) error {
	weigher, ok := w.ValidatableWeighers[spec.Name]
	if !ok {
		return fmt.Errorf("unknown weigher: %q", spec.Name)
	}
	return weigher.Validate(ctx, spec.Params)
}

// validateDetector validates a single detector specification.
func (w *PipelineAdmissionWebhook) validateDetector(ctx context.Context, spec v1alpha1.DetectorSpec) error {
	detector, ok := w.ValidatableDetectors[spec.Name]
	if !ok {
		return fmt.Errorf("unknown detector: %q", spec.Name)
	}
	return detector.Validate(ctx, spec.Params)
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
