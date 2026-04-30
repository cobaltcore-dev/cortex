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

// resourceProviderAggregatesResponse is the JSON body returned by
// GET /resource_providers/{uuid}/aggregates and
// PUT /resource_providers/{uuid}/aggregates (microversion 1.19+).
//
// https://docs.openstack.org/api-ref/placement/#resource-provider-aggregates
type resourceProviderAggregatesResponse struct {
	Aggregates                 []string `json:"aggregates"`
	ResourceProviderGeneration int64    `json:"resource_provider_generation"`
}

// resourceProviderAggregatesRequest is the JSON body expected by
// PUT /resource_providers/{uuid}/aggregates (microversion 1.19+).
type resourceProviderAggregatesRequest struct {
	Aggregates                 []string `json:"aggregates"`
	ResourceProviderGeneration int64    `json:"resource_provider_generation"`
}

// HandleListResourceProviderAggregates handles
// GET /resource_providers/{uuid}/aggregates requests.
//
// Returns the list of aggregate UUIDs associated with the resource provider.
// Aggregates model relationships among providers such as shared storage,
// affinity/anti-affinity groups, and availability zones. Returns an empty
// list if the provider has no aggregate associations.
//
// Routing: the uuid is used to determine if the resource provider is a KVM
// hypervisor or vmware/ironic hypervisor. Passthrough mode forwards all
// requests to upstream placement. Hybrid mode uses the hypervisor CRD for
// KVM hypervisors and forwards for anything else. CRD-only mode rejects
// any non-KVM calls with 404.
//
// https://docs.openstack.org/api-ref/placement/#list-resource-provider-aggregates
func (s *Shim) HandleListResourceProviderAggregates(w http.ResponseWriter, r *http.Request) {
	uuid, ok := requiredUUIDPathParam(w, r, "uuid")
	if !ok {
		return
	}
	switch s.featureModeFromConfOrHeader(r, s.config.Features.Aggregates) {
	case FeatureModePassthrough:
		s.forward(w, r)
	case FeatureModeHybrid:
		s.listResourceProviderAggregatesHybrid(w, r, uuid)
	case FeatureModeCRD:
		s.listResourceProviderAggregatesCRD(w, r, uuid)
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
	}
}

// listResourceProviderAggregatesHybrid serves from the CRD if the provider is
// a KVM hypervisor, otherwise forwards to upstream placement.
func (s *Shim) listResourceProviderAggregatesHybrid(w http.ResponseWriter, r *http.Request, uuid string) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	var hvs hv1.HypervisorList
	err := s.List(ctx, &hvs, client.MatchingFields{idxHypervisorOpenStackId: uuid})
	if err != nil || len(hvs.Items) != 1 {
		log.Info("resource provider not resolved from kubernetes, forwarding to upstream placement", "uuid", uuid)
		s.forward(w, r)
		return
	}
	log.Info("resolved resource provider from CRD, serving aggregates", "uuid", uuid, "hypervisor", hvs.Items[0].Name)
	s.writeAggregatesFromCRD(w, &hvs.Items[0])
}

// listResourceProviderAggregatesCRD serves exclusively from the CRD, returning
// 404 if the provider is not a known KVM hypervisor.
func (s *Shim) listResourceProviderAggregatesCRD(w http.ResponseWriter, r *http.Request, uuid string) {
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
	log.Info("serving aggregates from CRD", "uuid", uuid, "hypervisor", hvs.Items[0].Name)
	s.writeAggregatesFromCRD(w, &hvs.Items[0])
}

func (s *Shim) writeAggregatesFromCRD(w http.ResponseWriter, hv *hv1.Hypervisor) {
	aggGroups := hv1.GetAggregates(hv.Spec.Groups)
	aggregates := make([]string, 0, len(aggGroups))
	for _, ag := range aggGroups {
		aggregates = append(aggregates, ag.UUID)
	}
	s.writeJSON(w, http.StatusOK, resourceProviderAggregatesResponse{
		Aggregates:                 aggregates,
		ResourceProviderGeneration: hv.Generation,
	})
}

// HandleUpdateResourceProviderAggregates handles
// PUT /resource_providers/{uuid}/aggregates requests.
//
// Replaces the complete set of aggregate associations for a resource provider.
// The request body must include an aggregates array and a
// resource_provider_generation for optimistic concurrency control. Returns
// 409 Conflict if the generation does not match. Returns 200 with the
// updated aggregate list on success.
//
// Routing: same selective per-provider dispatch as GET.
//
// https://docs.openstack.org/api-ref/placement/#update-resource-provider-aggregates
func (s *Shim) HandleUpdateResourceProviderAggregates(w http.ResponseWriter, r *http.Request) {
	uuid, ok := requiredUUIDPathParam(w, r, "uuid")
	if !ok {
		return
	}
	switch s.featureModeFromConfOrHeader(r, s.config.Features.Aggregates) {
	case FeatureModePassthrough:
		s.forward(w, r)
	case FeatureModeHybrid:
		s.updateResourceProviderAggregatesHybrid(w, r, uuid)
	case FeatureModeCRD:
		s.updateResourceProviderAggregatesCRD(w, r, uuid)
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
	}
}

// updateResourceProviderAggregatesHybrid updates aggregates via the CRD if the
// provider is a KVM hypervisor, otherwise forwards to upstream placement.
func (s *Shim) updateResourceProviderAggregatesHybrid(w http.ResponseWriter, r *http.Request, uuid string) {
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
	log.Info("resolved resource provider from CRD, updating aggregates", "uuid", uuid, "hypervisor", hv.Name)

	var req resourceProviderAggregatesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "malformed request body", http.StatusBadRequest)
		return
	}
	if req.ResourceProviderGeneration != hv.Generation {
		log.Info("generation mismatch on aggregate update",
			"expected", req.ResourceProviderGeneration, "actual", hv.Generation)
		http.Error(w, "resource provider generation conflict", http.StatusConflict)
		return
	}

	var newGroups []hv1.Group
	for i := range hv.Spec.Groups {
		if hv.Spec.Groups[i].Aggregate == nil {
			newGroups = append(newGroups, hv.Spec.Groups[i])
		}
	}
	for _, aggUUID := range req.Aggregates {
		newGroups = append(newGroups, hv1.Group{
			Aggregate: &hv1.AggregateGroup{Name: aggUUID, UUID: aggUUID},
		})
	}
	hv.Spec.Groups = newGroups

	if err := s.Update(ctx, hv); err != nil {
		if apierrors.IsConflict(err) {
			http.Error(w, "resource provider generation conflict", http.StatusConflict)
			return
		}
		log.Error(err, "failed to update hypervisor aggregates")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	log.Info("successfully updated aggregates via CRD", "uuid", uuid, "aggregateCount", len(req.Aggregates))
	s.writeJSON(w, http.StatusOK, resourceProviderAggregatesResponse{
		Aggregates:                 req.Aggregates,
		ResourceProviderGeneration: hv.Generation,
	})
}

// updateResourceProviderAggregatesCRD updates aggregates exclusively via the
// CRD, returning 404 if the provider is not a known KVM hypervisor.
func (s *Shim) updateResourceProviderAggregatesCRD(w http.ResponseWriter, r *http.Request, uuid string) {
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
	log.Info("updating aggregates via CRD", "uuid", uuid, "hypervisor", hv.Name)

	var req resourceProviderAggregatesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "malformed request body", http.StatusBadRequest)
		return
	}
	if req.ResourceProviderGeneration != hv.Generation {
		log.Info("generation mismatch on aggregate update",
			"expected", req.ResourceProviderGeneration, "actual", hv.Generation)
		http.Error(w, "resource provider generation conflict", http.StatusConflict)
		return
	}

	var newGroups []hv1.Group
	for i := range hv.Spec.Groups {
		if hv.Spec.Groups[i].Aggregate == nil {
			newGroups = append(newGroups, hv.Spec.Groups[i])
		}
	}
	for _, aggUUID := range req.Aggregates {
		newGroups = append(newGroups, hv1.Group{
			Aggregate: &hv1.AggregateGroup{Name: aggUUID, UUID: aggUUID},
		})
	}
	hv.Spec.Groups = newGroups

	if err := s.Update(ctx, hv); err != nil {
		if apierrors.IsConflict(err) {
			http.Error(w, "resource provider generation conflict", http.StatusConflict)
			return
		}
		log.Error(err, "failed to update hypervisor aggregates")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	log.Info("successfully updated aggregates via CRD", "uuid", uuid, "aggregateCount", len(req.Aggregates))
	s.writeJSON(w, http.StatusOK, resourceProviderAggregatesResponse{
		Aggregates:                 req.Aggregates,
		ResourceProviderGeneration: hv.Generation,
	})
}
