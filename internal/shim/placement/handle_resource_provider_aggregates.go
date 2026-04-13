// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// HandleListResourceProviderAggregates handles
// GET /resource_providers/{uuid}/aggregates requests.
//
// Returns the list of aggregate UUIDs associated with the resource provider.
// Aggregates model relationships among providers such as shared storage,
// affinity/anti-affinity groups, and availability zones. Returns an empty
// list if the provider has no aggregate associations. Available since
// microversion 1.1.
//
// The response format changed at microversion 1.19: earlier versions return
// only a flat array of UUIDs, while 1.19+ returns an object that also
// includes the resource_provider_generation for concurrency tracking. Returns
// 404 if the provider does not exist.
func (s *Shim) HandleListResourceProviderAggregates(w http.ResponseWriter, r *http.Request) {
	uuid, ok := requiredUUIDPathParam(w, r, "uuid")
	if !ok {
		return
	}
	ctx := r.Context()
	log := logf.FromContext(ctx)
	log.Info("placement request", "method", r.Method, "path", r.URL.Path, "uuid", uuid)
	s.forward(w, r)
}

// HandleUpdateResourceProviderAggregates handles
// PUT /resource_providers/{uuid}/aggregates requests.
//
// Replaces the complete set of aggregate associations for a resource provider.
// Any aggregate UUIDs that do not yet exist are created automatically. The
// request format changed at microversion 1.19: earlier versions accept a
// plain array of UUIDs, while 1.19+ expects an object containing an
// aggregates array and a resource_provider_generation for optimistic
// concurrency control. Returns 409 Conflict if the generation does not match
// (1.19+). Returns 200 with the updated aggregate list on success.
func (s *Shim) HandleUpdateResourceProviderAggregates(w http.ResponseWriter, r *http.Request) {
	uuid, ok := requiredUUIDPathParam(w, r, "uuid")
	if !ok {
		return
	}
	ctx := r.Context()
	log := logf.FromContext(ctx)
	log.Info("placement request", "method", r.Method, "path", r.URL.Path, "uuid", uuid)
	s.forward(w, r)
}
