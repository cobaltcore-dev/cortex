// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"encoding/json"
	"net/http"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// resourceProviderTraitsResponse is the JSON body returned by
// GET /resource_providers/{uuid}/traits and
// PUT /resource_providers/{uuid}/traits.
//
// https://docs.openstack.org/api-ref/placement/#resource-provider-traits
type resourceProviderTraitsResponse struct {
	Traits                     []string `json:"traits"`
	ResourceProviderGeneration int64    `json:"resource_provider_generation"`
}

// resourceProviderTraitsRequest is the JSON body expected by
// PUT /resource_providers/{uuid}/traits.
type resourceProviderTraitsRequest struct {
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
//
// https://docs.openstack.org/api-ref/placement/#list-resource-provider-traits
func (s *Shim) HandleListResourceProviderTraits(w http.ResponseWriter, r *http.Request) {
	uuid, ok := requiredUUIDPathParam(w, r, "uuid")
	if !ok {
		return
	}
	switch s.featureModeFromConfOrHeader(r, s.config.Features.ResourceProviderTraits, true) {
	case FeatureModePassthrough:
		s.forward(w, r)
	case FeatureModeHybrid:
		s.listResourceProviderTraitsHybrid(w, r, uuid)
	case FeatureModeCRD:
		s.listResourceProviderTraitsCRD(w, r, uuid)
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
	}
}

// listResourceProviderTraitsHybrid serves from the CRD if the provider is a
// KVM hypervisor, otherwise forwards to upstream placement.
func (s *Shim) listResourceProviderTraitsHybrid(w http.ResponseWriter, r *http.Request, uuid string) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	var hvs hv1.HypervisorList
	err := s.List(ctx, &hvs, client.MatchingFields{idxHypervisorOpenStackId: uuid})
	if err != nil || len(hvs.Items) != 1 {
		log.Info("resource provider not resolved from kubernetes, forwarding to upstream placement", "uuid", uuid)
		s.forward(w, r)
		return
	}
	log.Info("resolved resource provider from CRD, serving traits", "uuid", uuid, "hypervisor", hvs.Items[0].Name)
	s.writeTraitsFromCRD(w, &hvs.Items[0])
}

// listResourceProviderTraitsCRD serves exclusively from the CRD, returning 404
// if the provider is not a known KVM hypervisor.
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
	log.Info("serving traits from CRD", "uuid", uuid, "hypervisor", hvs.Items[0].Name)
	s.writeTraitsFromCRD(w, &hvs.Items[0])
}

func (s *Shim) writeTraitsFromCRD(w http.ResponseWriter, hv *hv1.Hypervisor) {
	traitGroups := hv1.GetTraits(hv.Spec.Groups)
	traits := make([]string, 0, len(traitGroups))
	for _, tg := range traitGroups {
		traits = append(traits, tg.Name)
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
// Returns 409 Conflict if the generation does not match.
//
// https://docs.openstack.org/api-ref/placement/#update-resource-provider-traits
func (s *Shim) HandleUpdateResourceProviderTraits(w http.ResponseWriter, r *http.Request) {
	uuid, ok := requiredUUIDPathParam(w, r, "uuid")
	if !ok {
		return
	}
	switch s.featureModeFromConfOrHeader(r, s.config.Features.ResourceProviderTraits, true) {
	case FeatureModePassthrough:
		s.forward(w, r)
	case FeatureModeHybrid:
		s.updateResourceProviderTraitsHybrid(w, r, uuid)
	case FeatureModeCRD:
		s.updateResourceProviderTraitsCRD(w, r, uuid)
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
	}
}

// updateResourceProviderTraitsHybrid updates traits via the CRD if the
// provider is a KVM hypervisor, otherwise forwards to upstream placement.
func (s *Shim) updateResourceProviderTraitsHybrid(w http.ResponseWriter, r *http.Request, uuid string) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	var hvs hv1.HypervisorList
	err := s.List(ctx, &hvs, client.MatchingFields{idxHypervisorOpenStackId: uuid})
	if err != nil || len(hvs.Items) != 1 {
		log.Info("resource provider not resolved from kubernetes, forwarding to upstream placement", "uuid", uuid)
		s.forward(w, r)
		return
	}
	hv := &hvs.Items[0]
	log.Info("resolved resource provider from CRD, updating traits", "uuid", uuid, "hypervisor", hv.Name)

	var req resourceProviderTraitsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "malformed request body", http.StatusBadRequest)
		return
	}
	if req.ResourceProviderGeneration != hv.Generation {
		log.Info("generation mismatch on trait update",
			"expected", req.ResourceProviderGeneration, "actual", hv.Generation)
		http.Error(w, "resource provider generation conflict", http.StatusConflict)
		return
	}

	var newGroups []hv1.Group
	for i := range hv.Spec.Groups {
		if hv.Spec.Groups[i].Trait == nil {
			newGroups = append(newGroups, hv.Spec.Groups[i])
		}
	}
	for _, name := range req.Traits {
		newGroups = append(newGroups, hv1.Group{
			Trait: &hv1.TraitGroup{Name: name},
		})
	}
	hv.Spec.Groups = newGroups

	if err := s.Update(ctx, hv); err != nil {
		if apierrors.IsConflict(err) {
			http.Error(w, "resource provider generation conflict", http.StatusConflict)
			return
		}
		log.Error(err, "failed to update hypervisor traits")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	log.Info("successfully updated traits via CRD", "uuid", uuid, "traitCount", len(req.Traits))
	s.writeJSON(w, http.StatusOK, resourceProviderTraitsResponse{
		Traits:                     req.Traits,
		ResourceProviderGeneration: hv.Generation,
	})
}

// updateResourceProviderTraitsCRD updates traits exclusively via the CRD,
// returning 404 if the provider is not a known KVM hypervisor.
func (s *Shim) updateResourceProviderTraitsCRD(w http.ResponseWriter, r *http.Request, uuid string) {
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
	hv := &hvs.Items[0]
	log.Info("updating traits via CRD", "uuid", uuid, "hypervisor", hv.Name)

	var req resourceProviderTraitsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "malformed request body", http.StatusBadRequest)
		return
	}
	if req.ResourceProviderGeneration != hv.Generation {
		log.Info("generation mismatch on trait update",
			"expected", req.ResourceProviderGeneration, "actual", hv.Generation)
		http.Error(w, "resource provider generation conflict", http.StatusConflict)
		return
	}

	var newGroups []hv1.Group
	for i := range hv.Spec.Groups {
		if hv.Spec.Groups[i].Trait == nil {
			newGroups = append(newGroups, hv.Spec.Groups[i])
		}
	}
	for _, name := range req.Traits {
		newGroups = append(newGroups, hv1.Group{
			Trait: &hv1.TraitGroup{Name: name},
		})
	}
	hv.Spec.Groups = newGroups

	if err := s.Update(ctx, hv); err != nil {
		if apierrors.IsConflict(err) {
			http.Error(w, "resource provider generation conflict", http.StatusConflict)
			return
		}
		log.Error(err, "failed to update hypervisor traits")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	log.Info("successfully updated traits via CRD", "uuid", uuid, "traitCount", len(req.Traits))
	s.writeJSON(w, http.StatusOK, resourceProviderTraitsResponse{
		Traits:                     req.Traits,
		ResourceProviderGeneration: hv.Generation,
	})
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
//
// https://docs.openstack.org/api-ref/placement/#delete-resource-provider-traits
func (s *Shim) HandleDeleteResourceProviderTraits(w http.ResponseWriter, r *http.Request) {
	uuid, ok := requiredUUIDPathParam(w, r, "uuid")
	if !ok {
		return
	}
	switch s.featureModeFromConfOrHeader(r, s.config.Features.ResourceProviderTraits, true) {
	case FeatureModePassthrough:
		s.forward(w, r)
	case FeatureModeHybrid:
		s.deleteResourceProviderTraitsHybrid(w, r, uuid)
	case FeatureModeCRD:
		s.deleteResourceProviderTraitsCRD(w, r, uuid)
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
	}
}

// deleteResourceProviderTraitsHybrid removes all traits via the CRD if the
// provider is a KVM hypervisor, otherwise forwards to upstream placement.
func (s *Shim) deleteResourceProviderTraitsHybrid(w http.ResponseWriter, r *http.Request, uuid string) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	var hvs hv1.HypervisorList
	err := s.List(ctx, &hvs, client.MatchingFields{idxHypervisorOpenStackId: uuid})
	if err != nil || len(hvs.Items) != 1 {
		log.Info("resource provider not resolved from kubernetes, forwarding to upstream placement", "uuid", uuid)
		s.forward(w, r)
		return
	}
	hv := &hvs.Items[0]
	log.Info("resolved resource provider from CRD, deleting traits", "uuid", uuid, "hypervisor", hv.Name)

	var newGroups []hv1.Group
	for i := range hv.Spec.Groups {
		if hv.Spec.Groups[i].Trait == nil {
			newGroups = append(newGroups, hv.Spec.Groups[i])
		}
	}
	hv.Spec.Groups = newGroups

	if err := s.Update(ctx, hv); err != nil {
		if apierrors.IsConflict(err) {
			http.Error(w, "resource provider generation conflict", http.StatusConflict)
			return
		}
		log.Error(err, "failed to delete hypervisor traits")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	log.Info("successfully deleted all traits via CRD", "uuid", uuid)
	w.WriteHeader(http.StatusNoContent)
}

// deleteResourceProviderTraitsCRD removes all traits exclusively via the CRD,
// returning 404 if the provider is not a known KVM hypervisor.
func (s *Shim) deleteResourceProviderTraitsCRD(w http.ResponseWriter, r *http.Request, uuid string) {
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
	hv := &hvs.Items[0]
	log.Info("deleting all traits via CRD", "uuid", uuid, "hypervisor", hv.Name)

	var newGroups []hv1.Group
	for i := range hv.Spec.Groups {
		if hv.Spec.Groups[i].Trait == nil {
			newGroups = append(newGroups, hv.Spec.Groups[i])
		}
	}
	hv.Spec.Groups = newGroups

	if err := s.Update(ctx, hv); err != nil {
		if apierrors.IsConflict(err) {
			http.Error(w, "resource provider generation conflict", http.StatusConflict)
			return
		}
		log.Error(err, "failed to delete hypervisor traits")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	log.Info("successfully deleted all traits via CRD", "uuid", uuid)
	w.WriteHeader(http.StatusNoContent)
}
