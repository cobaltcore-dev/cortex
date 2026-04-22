// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// resourceProvider appears in the body of a successful
// /resource_providers/{uuid} response.
type resourceProvider struct {
	// A consistent view marker that assists with the management of concurrent
	// resource provider updates.
	Generation int64 `json:"generation"`
	// The uuid of a resource provider.
	UUID string `json:"uuid"`
	// A list of links associated with one resource provider.
	Links []resourceProviderLink `json:"links"`
	// The name of a resource provider.
	Name string `json:"name"`
	// The uuid of the parent resource provider, if any.
	ParentProviderUUID *string `json:"parent_provider_uuid,omitempty"`
	// The uuid of the root resource provider in the tree, if any.
	RootProviderUUID *string `json:"root_provider_uuid,omitempty"`
}

// resourceProviderLink describes a link to a related object in the
// response to /resource_providers/{uuid}.
type resourceProviderLink struct {
	// The relation of the linked object to the resource provider.
	Rel string `json:"rel"`
	// The URL of the linked object.
	Href string `json:"href"`
}

// translateToResourceProvider constructs a resourceProvider from a Hypervisor.
// KVM hypervisors are root providers with no parent.
func translateToResourceProvider(hv hv1.Hypervisor) resourceProvider {
	return resourceProvider{
		Generation:       hv.Generation,
		UUID:             hv.Status.HypervisorID,
		Name:             hv.Name,
		RootProviderUUID: &hv.Status.HypervisorID,
		Links: []resourceProviderLink{
			{
				Rel:  "self",
				Href: "/resource_providers/" + hv.Status.HypervisorID,
			},
			{
				Rel:  "aggregates",
				Href: "/resource_providers/" + hv.Status.HypervisorID + "/aggregates",
			},
			{
				Rel:  "inventories",
				Href: "/resource_providers/" + hv.Status.HypervisorID + "/inventories",
			},
			{
				Rel:  "allocations",
				Href: "/resource_providers/" + hv.Status.HypervisorID + "/allocations",
			},
			{
				Rel:  "traits",
				Href: "/resource_providers/" + hv.Status.HypervisorID + "/traits",
			},
			{
				Rel:  "usages",
				Href: "/resource_providers/" + hv.Status.HypervisorID + "/usages",
			},
		},
	}
}

// createResourceProviderRequest is the expected JSON body for
// POST /resource_providers.
type createResourceProviderRequest struct {
	Name string `json:"name"`
	UUID string `json:"uuid,omitempty"`
}

// HandleCreateResourceProvider handles POST /resource_providers requests.
//
// Creates a new resource provider. The request must include a name and may
// optionally specify a UUID and a parent_provider_uuid (since 1.14) to place
// the provider in a hierarchical tree. If no UUID is supplied, one is
// generated. Before microversion 1.37, the parent of a resource provider
// could not be changed after creation.
//
// The response changed at microversion 1.20: earlier versions return only
// an HTTP 201 with a Location header, while 1.20+ returns the full resource
// provider object in the body. Returns 409 Conflict if a provider with the
// same name or UUID already exists.
//
// If the name matches a KVM hypervisor already managed by Kubernetes, the
// shim returns 409 Conflict to prevent shadow resource providers from being
// created in upstream placement.
//
// See: https://docs.openstack.org/api-ref/placement/#create-resource-provider
func (s *Shim) HandleCreateResourceProvider(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	if !s.config.Features.EnableResourceProviders {
		s.forward(w, r)
		return
	}

	// Buffer the body so we can decode it and still forward the original
	// bytes to upstream placement.
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.Error(err, "failed to read request body")
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	var req createResourceProviderRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "missing required field: name", http.StatusBadRequest)
		return
	}

	// Look up by name — the fast path using the name index.
	// Names are unique, so we expect at most one result.
	var hvs hv1.HypervisorList
	err = s.List(ctx, &hvs, client.MatchingFields{idxHypervisorName: req.Name})
	if err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "failed to list hypervisors with name index")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if len(hvs.Items) > 1 {
		log.Error(nil, "multiple hypervisors found with the same name", "name", req.Name)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if len(hvs.Items) == 1 {
		log.Error(nil, "attempt to create a resource provider that conflicts with a kvm hypervisor",
			"name", req.Name, "hypervisorID", hvs.Items[0].Status.HypervisorID)
		http.Error(w, "conflict with an existing kvm hypervisor resource provider", http.StatusConflict)
		return
	}

	// Check UUID collision with existing KVM hypervisors.
	if req.UUID != "" {
		var hvsByUUID hv1.HypervisorList
		err = s.List(ctx, &hvsByUUID, client.MatchingFields{idxHypervisorOpenStackId: req.UUID})
		if err != nil && !apierrors.IsNotFound(err) {
			log.Error(err, "failed to list hypervisors with OpenStack ID index")
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		if len(hvsByUUID.Items) > 0 {
			log.Error(nil, "attempt to create a resource provider that conflicts with a kvm hypervisor UUID",
				"uuid", req.UUID)
			http.Error(w, "conflict with an existing kvm hypervisor resource provider", http.StatusConflict)
			return
		}
	}

	// No conflict — restore the body and forward to upstream placement.
	log.Info("no conflict with existing kvm hypervisor, forwarding create resource provider request to upstream placement",
		"name", req.Name)
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	s.forward(w, r)
}

// HandleShowResourceProvider handles GET /resource_providers/{uuid} requests.
//
// Returns a single resource provider identified by its UUID. The response
// includes the provider's name, generation (used for concurrency control in
// subsequent updates), and links. Starting at microversion 1.14, the response
// also includes parent_provider_uuid and root_provider_uuid to describe the
// provider's position in a hierarchical tree. Returns 404 if the provider
// does not exist.
//
// See: https://docs.openstack.org/api-ref/placement/#show-resource-provider
func (s *Shim) HandleShowResourceProvider(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	if !s.config.Features.EnableResourceProviders {
		s.forward(w, r)
		return
	}

	uuid, ok := requiredUUIDPathParam(w, r, "uuid")
	if !ok {
		return
	}

	// Try to find the hypervisor in kubernetes.
	var hvs hv1.HypervisorList
	err := s.List(ctx, &hvs, client.MatchingFields{idxHypervisorOpenStackId: uuid})
	if apierrors.IsNotFound(err) || len(hvs.Items) == 0 {
		// Forward the request to placement if the hypervisor doesn't exist.
		log.Info("resource provider not found in kubernetes, forwarding to upstream placement",
			"uuid", uuid)
		s.forward(w, r)
		return
	}
	if err != nil {
		// Something else is wrong.
		log.Error(err, "failed to list hypervisors with OpenStack ID index")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if len(hvs.Items) > 1 {
		log.Error(nil, "multiple hypervisors found with the same OpenStack ID", "uuid", uuid)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Translate the hypervisor to a resource provider response.
	log.Info("resource provider found in kubernetes, returning translated kvm hypervisor",
		"uuid", uuid, "hypervisor", hvs.Items[0].Name)
	s.writeJSON(w, http.StatusOK, translateToResourceProvider(hvs.Items[0]))
}

// updateResourceProviderRequest is the expected JSON body for
// PUT /resource_providers/{uuid}.
type updateResourceProviderRequest struct {
	Name               string  `json:"name"`
	ParentProviderUUID *string `json:"parent_provider_uuid,omitempty"`
}

// HandleUpdateResourceProvider handles PUT /resource_providers/{uuid} requests.
//
// Updates a resource provider's name and, starting at microversion 1.14, its
// parent_provider_uuid. Since microversion 1.37, the parent may be changed to
// any existing provider UUID that would not create a loop in the tree, or set
// to null to make the provider a root. Returns 409 Conflict if another
// provider already has the requested name.
//
// See: https://docs.openstack.org/api-ref/placement/#update-resource-provider
func (s *Shim) HandleUpdateResourceProvider(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	if !s.config.Features.EnableResourceProviders {
		s.forward(w, r)
		return
	}

	uuid, ok := requiredUUIDPathParam(w, r, "uuid")
	if !ok {
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.Error(err, "failed to read request body")
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	var req updateResourceProviderRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "missing required field: name", http.StatusBadRequest)
		return
	}

	// Try to find the hypervisor in kubernetes.
	var hvs hv1.HypervisorList
	err = s.List(ctx, &hvs, client.MatchingFields{idxHypervisorOpenStackId: uuid})
	if apierrors.IsNotFound(err) || len(hvs.Items) == 0 {
		// Forward the request to placement if the hypervisor doesn't exist.
		log.Info("resource provider not found in kubernetes, forwarding to upstream placement",
			"uuid", uuid)
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		s.forward(w, r)
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

	// The hypervisor's name is immutable, so if the request tries to change it,
	// we return a 409 Conflict to match the behavior of placement.
	if hv.Name != req.Name {
		log.Error(nil, "attempt to change the name of a kvm hypervisor resource provider", "uuid", uuid, "currentName", hv.Name, "requestedName", req.Name)
		http.Error(w, "cannot change the name of a kvm hypervisor resource provider", http.StatusConflict)
		return
	}
	// KVM hypervisors are root providers with no parent. Any attempt to set a
	// parent is rejected.
	if req.ParentProviderUUID != nil {
		log.Error(nil, "attempt to set parent on a kvm hypervisor resource provider", "uuid", uuid, "requestedParent", *req.ParentProviderUUID)
		http.Error(w, "cannot change the parent of a kvm hypervisor resource provider", http.StatusConflict)
		return
	}

	// If we get here, the request is valid but doesn't actually change anything,
	// so we can just return the current state of the resource provider.
	log.Info("update to kvm hypervisor resource provider has no effect, returning current state",
		"uuid", uuid, "name", hv.Name, "parentProviderUUID", hv.Status.HypervisorID)
	s.writeJSON(w, http.StatusOK, translateToResourceProvider(hv))
}

// HandleDeleteResourceProvider handles DELETE /resource_providers/{uuid} requests.
//
// Deletes a resource provider and disassociates all its aggregates and
// inventories. The operation fails with 409 Conflict if there are any
// allocations against the provider's inventories or if the provider has
// child providers in a tree hierarchy. Returns 204 No Content on success.
//
// See: https://docs.openstack.org/api-ref/placement/#delete-resource-provider
func (s *Shim) HandleDeleteResourceProvider(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	if !s.config.Features.EnableResourceProviders {
		s.forward(w, r)
		return
	}

	uuid, ok := requiredUUIDPathParam(w, r, "uuid")
	if !ok {
		return
	}

	// Try to find the hypervisor in kubernetes.
	var hvs hv1.HypervisorList
	err := s.List(ctx, &hvs, client.MatchingFields{idxHypervisorOpenStackId: uuid})
	if apierrors.IsNotFound(err) || len(hvs.Items) == 0 {
		// Forward the request to placement if the hypervisor doesn't exist.
		log.Info("resource provider not found in kubernetes, forwarding to upstream placement",
			"uuid", uuid)
		s.forward(w, r)
		return
	}
	if err != nil {
		// Something else is wrong.
		log.Error(err, "failed to list hypervisors with OpenStack ID index")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if len(hvs.Items) > 1 {
		log.Error(nil, "multiple hypervisors found with the same OpenStack ID", "uuid", uuid)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// KVM hypervisor resources are immutable to the extent that they cannot be
	// deleted, so we return a 409 Conflict to match the behavior of placement.
	log.Error(nil, "attempt to delete a kvm hypervisor resource provider", "uuid", uuid)
	http.Error(w, "cannot delete a kvm hypervisor resource provider", http.StatusConflict)
}

// listResourceProvidersResponse is the JSON envelope returned by
// GET /resource_providers.
type listResourceProvidersResponse struct {
	ResourceProviders []resourceProvider `json:"resource_providers"`
}

// HandleListResourceProviders handles GET /resource_providers requests.
//
// Returns a filtered list of resource providers. Resource providers are
// entities that provide consumable inventory of one or more classes of
// resources (e.g. a compute node providing VCPU, MEMORY_MB, DISK_GB).
//
// Supports numerous filter parameters including name, uuid, member_of
// (aggregate membership), resources (capacity filtering), in_tree (provider
// tree membership), and required (trait filtering). Multiple filters are
// combined with boolean AND logic. Many of these filters were added in later
// microversions: resources filtering at 1.3, tree queries at 1.14, trait
// requirements at 1.18, forbidden traits at 1.22, forbidden aggregates at
// 1.32, and the in: syntax for required at 1.39.
//
// The shim fetches resource providers from upstream placement, then merges
// in KVM hypervisors managed by Kubernetes. On uuid or name collisions the
// Kubernetes version wins and a warning is logged.
//
// See: https://docs.openstack.org/api-ref/placement/#list-resource-providers
func (s *Shim) HandleListResourceProviders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	if !s.config.Features.EnableResourceProviders {
		s.forward(w, r)
		return
	}

	s.forwardWithHook(w, r, func(w http.ResponseWriter, resp *http.Response) {
		if resp.StatusCode != http.StatusOK {
			for k, vs := range resp.Header {
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(resp.StatusCode)
			io.Copy(w, resp.Body) //nolint:errcheck
			return
		}

		var upstreamList listResourceProvidersResponse
		if err := json.NewDecoder(resp.Body).Decode(&upstreamList); err != nil {
			log.Error(err, "failed to decode upstream resource providers response")
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		var uuids []string
		for _, rp := range upstreamList.ResourceProviders {
			uuids = append(uuids, rp.UUID)
		}
		log.Info("fetched resource providers from upstream placement",
			"count", len(upstreamList.ResourceProviders), "uuids", uuids)

		// Fetch all KVM hypervisors from Kubernetes.
		query := r.URL.Query()
		var hvs hv1.HypervisorList
		if err := s.List(ctx, &hvs); err != nil && !apierrors.IsNotFound(err) {
			log.Error(err, "failed to list hypervisors")
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		uuids = nil
		for _, hv := range hvs.Items {
			uuids = append(uuids, hv.Status.HypervisorID)
		}
		log.Info("fetched hypervisors from kubernetes",
			"count", len(hvs.Items), "uuids", uuids)

		// Post-filter by query parameters.
		filtered := hvs.Items
		if v := query.Get("uuid"); v != "" {
			filtered = filterHypervisorsByUUID(ctx, filtered, v)
		}
		if v := query.Get("name"); v != "" {
			filtered = filterHypervisorsByName(ctx, filtered, v)
		}
		if vals := query["member_of"]; len(vals) > 0 {
			filtered = filterHypervisorsByMemberOf(ctx, filtered, vals)
		}
		if v := query.Get("in_tree"); v != "" {
			filtered = filterHypervisorsByInTree(ctx, filtered, v)
		}
		if vals := query["required"]; len(vals) > 0 {
			filtered = filterHypervisorsByRequired(ctx, filtered, vals)
		}
		if v := query.Get("resources"); v != "" {
			var err error
			filtered, err = filterHypervisorsByResources(ctx, filtered, v)
			if err != nil {
				log.Info("invalid resources query parameter", "error", err)
				http.Error(w, "invalid resources query parameter: "+err.Error(), http.StatusBadRequest)
				return
			}
		}

		// Build collision sets from filtered k8s hypervisors.
		k8sByUUID := make(map[string]resourceProvider, len(filtered))
		k8sByName := make(map[string]resourceProvider, len(filtered))
		for _, hv := range filtered {
			rp := translateToResourceProvider(hv)
			k8sByUUID[rp.UUID] = rp
			k8sByName[rp.Name] = rp
		}

		// Merge: keep upstream entries that don't collide; k8s wins.
		merged := make([]resourceProvider, 0, len(upstreamList.ResourceProviders)+len(k8sByUUID))
		for _, rp := range upstreamList.ResourceProviders {
			if _, ok := k8sByUUID[rp.UUID]; ok {
				log.Info("upstream resource provider uuid collides with kvm hypervisor, using kubernetes version",
					"uuid", rp.UUID, "name", rp.Name)
				continue
			}
			if _, ok := k8sByName[rp.Name]; ok {
				log.Info("upstream resource provider name collides with kvm hypervisor, using kubernetes version",
					"name", rp.Name, "uuid", rp.UUID)
				continue
			}
			merged = append(merged, rp)
		}
		for _, rp := range k8sByUUID {
			merged = append(merged, rp)
		}

		log.Info("merged resource providers from upstream placement and kubernetes",
			"upstreamCount", len(upstreamList.ResourceProviders),
			"kubernetesCount", len(filtered),
			"mergedCount", len(merged),
		)
		s.writeJSON(w, http.StatusOK, listResourceProvidersResponse{
			ResourceProviders: merged,
		})
	})
}

func filterHypervisorsByUUID(ctx context.Context, hvs []hv1.Hypervisor, uuid string) []hv1.Hypervisor {
	log := logf.FromContext(ctx)
	out := make([]hv1.Hypervisor, 0, 1)
	for _, hv := range hvs {
		if hv.Status.HypervisorID == uuid {
			out = append(out, hv)
		} else {
			log.V(1).Info("hypervisor filtered out by uuid",
				"hypervisor", hv.Name, "hypervisorID", hv.Status.HypervisorID, "wantUUID", uuid)
		}
	}
	return out
}

func filterHypervisorsByName(ctx context.Context, hvs []hv1.Hypervisor, name string) []hv1.Hypervisor {
	log := logf.FromContext(ctx)
	out := make([]hv1.Hypervisor, 0, 1)
	for _, hv := range hvs {
		if hv.Name == name {
			out = append(out, hv)
		} else {
			log.V(1).Info("hypervisor filtered out by name",
				"hypervisor", hv.Name, "wantName", name)
		}
	}
	return out
}

// filterHypervisorsByMemberOf applies AND logic across repeated member_of
// params. Each value can be:
//   - bare UUID
//   - in:uuid1,uuid2 (any-of)
//   - !uuid or !in:uuid1,uuid2 (forbidden)
func filterHypervisorsByMemberOf(ctx context.Context, hvs []hv1.Hypervisor, memberOf []string) []hv1.Hypervisor {
	log := logf.FromContext(ctx)
	for _, expr := range memberOf {
		forbidden := strings.HasPrefix(expr, "!")
		if forbidden {
			expr = expr[1:]
		}
		var uuids []string
		if strings.HasPrefix(expr, "in:") {
			uuids = strings.Split(expr[3:], ",")
		} else {
			uuids = []string{expr}
		}
		uuidSet := make(map[string]struct{}, len(uuids))
		for _, u := range uuids {
			uuidSet[u] = struct{}{}
		}

		out := make([]hv1.Hypervisor, 0, len(hvs))
		for _, hv := range hvs {
			member := false
			for _, agg := range hv.Status.Aggregates {
				if _, ok := uuidSet[agg.UUID]; ok {
					member = true
					break
				}
			}
			switch {
			case forbidden && !member:
				out = append(out, hv)
			case !forbidden && member:
				out = append(out, hv)
			default:
				log.V(1).Info("hypervisor filtered out by member_of",
					"hypervisor", hv.Name, "forbidden", forbidden, "member", member)
			}
		}
		hvs = out
	}
	return hvs
}

// filterHypervisorsByInTree keeps hypervisors whose UUID matches in_tree.
// KVM hypervisors are flat 1-element trees (root == self).
func filterHypervisorsByInTree(ctx context.Context, hvs []hv1.Hypervisor, inTree string) []hv1.Hypervisor {
	log := logf.FromContext(ctx)
	out := make([]hv1.Hypervisor, 0, 1)
	for _, hv := range hvs {
		if hv.Status.HypervisorID == inTree {
			out = append(out, hv)
		} else {
			log.V(1).Info("hypervisor filtered out by in_tree",
				"hypervisor", hv.Name, "hypervisorID", hv.Status.HypervisorID, "wantInTree", inTree)
		}
	}
	return out
}

// filterHypervisorsByRequired applies AND logic across repeated required
// params. Each value is a comma-separated list of traits:
//   - TRAIT_A,TRAIT_B — all must be present
//   - !TRAIT_C — must NOT be present
//   - in:TRAIT_X,TRAIT_Y — at least one must be present
func filterHypervisorsByRequired(ctx context.Context, hvs []hv1.Hypervisor, required []string) []hv1.Hypervisor {
	log := logf.FromContext(ctx)
	for _, expr := range required {
		parts := strings.Split(expr, ",")
		out := make([]hv1.Hypervisor, 0, len(hvs))
		for _, hv := range hvs {
			traitSet := make(map[string]struct{}, len(hv.Status.Traits))
			for _, t := range hv.Status.Traits {
				traitSet[t] = struct{}{}
			}
			if matchesTraitExpr(traitSet, parts) {
				out = append(out, hv)
			} else {
				log.V(1).Info("hypervisor filtered out by required",
					"hypervisor", hv.Name, "expr", expr)
			}
		}
		hvs = out
	}
	return hvs
}

// matchesTraitExpr checks whether a single repeated required parameter
// (already split on comma) is satisfied by the given trait set.
func matchesTraitExpr(traitSet map[string]struct{}, parts []string) bool {
	i := 0
	for i < len(parts) {
		p := parts[i]
		switch {
		case strings.HasPrefix(p, "!"):
			// Forbidden trait.
			if _, ok := traitSet[p[1:]]; ok {
				return false
			}
			i++
		case strings.HasPrefix(p, "in:"):
			// Any-of group: collect all tokens until the next non-plain token.
			anyOf := []string{p[3:]}
			for i+1 < len(parts) && !strings.HasPrefix(parts[i+1], "!") && !strings.HasPrefix(parts[i+1], "in:") {
				i++
				anyOf = append(anyOf, parts[i])
			}
			found := false
			for _, t := range anyOf {
				if _, ok := traitSet[t]; ok {
					found = true
					break
				}
			}
			if !found {
				return false
			}
			i++
		default:
			// Required trait (must be present).
			if _, ok := traitSet[p]; !ok {
				return false
			}
			i++
		}
	}
	return true
}

// filterHypervisorsByResources keeps only hypervisors whose effective
// capacity meets or exceeds every requested resource amount.
func filterHypervisorsByResources(ctx context.Context, hvs []hv1.Hypervisor, raw string) ([]hv1.Hypervisor, error) {
	log := logf.FromContext(ctx)
	requested := make(map[string]resource.Quantity)
	for part := range strings.SplitSeq(raw, ",") {
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid resources token %q", part)
		}
		amount, err := strconv.ParseInt(kv[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid amount in resources token %q: %w", part, err)
		}
		if amount < 0 {
			return nil, fmt.Errorf("negative amount in resources token %q", part)
		}
		mappedResourceName := ""
		var mappedResourceQuantity *resource.Quantity
		switch kv[0] {
		case "VCPU":
			mappedResourceName = "cpu"
			mappedResourceQuantity = resource.NewQuantity(amount, resource.DecimalSI)
		case "MEMORY_MB":
			if amount > math.MaxInt64/(1024*1024) {
				return nil, fmt.Errorf("amount overflows bytes in resources token %q", part)
			}
			mappedResourceName = "memory"
			mappedResourceQuantity = resource.NewQuantity(amount*1024*1024, resource.DecimalSI)
		case "DISK_GB":
			if amount > math.MaxInt64/(1024*1024*1024) {
				return nil, fmt.Errorf("amount overflows bytes in resources token %q", part)
			}
			mappedResourceName = "disk"
			mappedResourceQuantity = resource.NewQuantity(amount*1024*1024*1024, resource.DecimalSI)
		default:
			return nil, fmt.Errorf("invalid resource class in resources token %q", part)
		}
		requested[mappedResourceName] = *mappedResourceQuantity
	}

	out := make([]hv1.Hypervisor, 0, len(hvs))
	for _, hv := range hvs {
		// Check that all requested resources are satisfied by the
		// hypervisor's capacity.
		satisfied := true
		for r, amount := range requested {
			provided, ok := hv.Status.EffectiveCapacity[hv1.ResourceName(r)]
			if !ok {
				// Fallback to the physical capacity.
				provided, ok = hv.Status.Capacity[hv1.ResourceName(r)]
				if !ok {
					provided = *resource.NewQuantity(0, resource.DecimalSI)
				}
			}
			if provided.Cmp(amount) < 0 {
				satisfied = false
				break
			}
		}
		if satisfied {
			out = append(out, hv)
		} else {
			log.V(1).Info("hypervisor filtered out by resources",
				"hypervisor", hv.Name, "resources", raw)
		}
	}
	return out, nil
}
