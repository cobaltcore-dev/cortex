// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/cinder/plugins/filters"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/cinder/plugins/weighers"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

// Create a new pipeline admission webhook for the cinder scheduling domain,
// using the known filters, weighers and detectors for validation.
func NewPipelineWebhook() lib.PipelineAdmissionWebhook {
	validatableFilters := map[string]lib.Validatable{}
	for name, constructor := range filters.Index {
		validatableFilters[name] = constructor()
	}
	validatableWeighers := map[string]lib.Validatable{}
	for name, constructor := range weighers.Index {
		validatableWeighers[name] = constructor()
	}
	validatableDetectors := map[string]lib.Validatable{} // No detectors for cinder yet.
	return lib.PipelineAdmissionWebhook{
		SchedulingDomain:     v1alpha1.SchedulingDomainNova,
		ValidatableFilters:   validatableFilters,
		ValidatableWeighers:  validatableWeighers,
		ValidatableDetectors: validatableDetectors,
	}
}
