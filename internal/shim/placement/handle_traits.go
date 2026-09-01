// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"
)

// HandleListTraits handles GET /traits requests.
//
// See: https://docs.openstack.org/api-ref/placement/#list-traits
func (s *Shim) HandleListTraits(w http.ResponseWriter, r *http.Request) {
	if !s.config.Features.Traits {
		s.forward(w, r)
		return
	}
	http.Error(w, "not yet implemented", http.StatusNotImplemented)
}

// HandleShowTrait handles GET /traits/{name} requests.
//
// See: https://docs.openstack.org/api-ref/placement/#show-traits
func (s *Shim) HandleShowTrait(w http.ResponseWriter, r *http.Request) {
	if !s.config.Features.Traits {
		s.forward(w, r)
		return
	}
	if _, ok := requiredPathParam(w, r, "name"); !ok {
		return
	}
	http.Error(w, "not yet implemented", http.StatusNotImplemented)
}

// HandleUpdateTrait handles PUT /traits/{name} requests.
//
// See: https://docs.openstack.org/api-ref/placement/#update-trait
func (s *Shim) HandleUpdateTrait(w http.ResponseWriter, r *http.Request) {
	if !s.config.Features.Traits {
		s.forward(w, r)
		return
	}
	if _, ok := requiredPathParam(w, r, "name"); !ok {
		return
	}
	http.Error(w, "not yet implemented", http.StatusNotImplemented)
}

// HandleDeleteTrait handles DELETE /traits/{name} requests.
//
// See: https://docs.openstack.org/api-ref/placement/#delete-traits
func (s *Shim) HandleDeleteTrait(w http.ResponseWriter, r *http.Request) {
	if !s.config.Features.Traits {
		s.forward(w, r)
		return
	}
	if _, ok := requiredPathParam(w, r, "name"); !ok {
		return
	}
	http.Error(w, "not yet implemented", http.StatusNotImplemented)
}
