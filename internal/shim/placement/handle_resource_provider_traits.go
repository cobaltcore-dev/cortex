// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// resourceProviderTraitsResponse is the JSON body returned by
// GET /resource_providers/{uuid}/traits and
// PUT /resource_providers/{uuid}/traits.
type resourceProviderTraitsResponse struct {
	Traits                     []string `json:"traits"`
	ResourceProviderGeneration int64    `json:"resource_provider_generation"`
}

// HandleListResourceProviderTraits handles
// GET /resource_providers/{uuid}/traits requests.
//
// Returns the list of traits associated with the resource provider identified
// by {uuid}. The response includes an array of trait name strings and the
// resource_provider_generation for concurrency tracking. Returns 404 if the
// provider does not exist.
func (s *Shim) HandleListResourceProviderTraits(w http.ResponseWriter, r *http.Request) {
	uuid, ok := requiredUUIDPathParam(w, r, "uuid")
	if !ok {
		return
	}
	switch s.featureModeFromConfOrHeader(r, s.config.Features.ResourceProviderTraits) {
	case FeatureModePassthrough:
		s.forward(w, r)
	case FeatureModeHybrid:
		s.forward(w, r)
	case FeatureModeCRD:
		s.listResourceProviderTraitsCRD(w, r, uuid)
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
	}
}

func (s *Shim) listResourceProviderTraitsCRD(w http.ResponseWriter, r *http.Request, uuid string) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	var hvs hv1.HypervisorList
	err := s.List(ctx, &hvs, client.MatchingFields{idxHypervisorOpenStackId: uuid})
	if apierrors.IsNotFound(err) || len(hvs.Items) == 0 {
		log.Info("resource provider not found in kubernetes (crd mode)", "uuid", uuid)
		http.Error(w, "resource provider not found", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Error(err, "failed to list hypervisors with OpenStack ID index")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if len(hvs.Items) > 1 {
		log.Error(nil, "multiple hypervisors found with the same OpenStack ID", "uuid", uuid)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	hv := hvs.Items[0]
	traits := hv.Status.Traits
	if traits == nil {
		traits = []string{}
	}
	s.writeJSON(w, http.StatusOK, resourceProviderTraitsResponse{
		Traits:                     traits,
		ResourceProviderGeneration: hv.Generation,
	})
}

// HandleUpdateResourceProviderTraits handles
// PUT /resource_providers/{uuid}/traits requests.
//
// Replaces the complete set of trait associations for a resource provider.
// The request body must include a traits array and the
// resource_provider_generation for optimistic concurrency control. All
// previously associated traits are removed and replaced by the specified set.
// Returns 400 Bad Request if any of the specified traits are invalid (i.e.
// not returned by GET /traits). Returns 409 Conflict if the generation does
// not match.
func (s *Shim) HandleUpdateResourceProviderTraits(w http.ResponseWriter, r *http.Request) {
	if _, ok := requiredUUIDPathParam(w, r, "uuid"); !ok {
		return
	}
	switch s.featureModeFromConfOrHeader(r, s.config.Features.ResourceProviderTraits) {
	case FeatureModePassthrough:
		s.forward(w, r)
	case FeatureModeHybrid:
		s.forward(w, r)
	case FeatureModeCRD:
		http.Error(w, "crd mode is not yet implemented for resource provider trait writes", http.StatusNotImplemented)
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
	}
}

// HandleDeleteResourceProviderTraits handles
// DELETE /resource_providers/{uuid}/traits requests.
//
// Removes all trait associations from a resource provider. Because this
// endpoint does not accept a resource_provider_generation, it is not safe
// for concurrent use. In environments where multiple clients manage traits
// for the same provider, prefer PUT with an empty traits list instead.
// Returns 404 if the provider does not exist. Returns 409 Conflict on
// concurrent modification. Returns 204 No Content on success.
func (s *Shim) HandleDeleteResourceProviderTraits(w http.ResponseWriter, r *http.Request) {
	if _, ok := requiredUUIDPathParam(w, r, "uuid"); !ok {
		return
	}
	switch s.featureModeFromConfOrHeader(r, s.config.Features.ResourceProviderTraits) {
	case FeatureModePassthrough:
		s.forward(w, r)
	case FeatureModeHybrid:
		s.forward(w, r)
	case FeatureModeCRD:
		http.Error(w, "crd mode is not yet implemented for resource provider trait writes", http.StatusNotImplemented)
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
	}
}
