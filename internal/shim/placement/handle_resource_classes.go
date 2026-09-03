// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"
)

// HandleListResourceClasses handles GET /resource_classes requests.
//
// See: https://docs.openstack.org/api-ref/placement/#list-resource-classes
func (s *Shim) HandleListResourceClasses(w http.ResponseWriter, r *http.Request) {
	if !s.config.Features.ResourceClasses {
		s.forward(w, r)
		return
	}
	http.Error(w, "not yet implemented", http.StatusNotImplemented)
}

// HandleCreateResourceClass handles POST /resource_classes requests.
//
// See: https://docs.openstack.org/api-ref/placement/#create-resource-class
func (s *Shim) HandleCreateResourceClass(w http.ResponseWriter, r *http.Request) {
	if !s.config.Features.ResourceClasses {
		s.forward(w, r)
		return
	}
	http.Error(w, "not yet implemented", http.StatusNotImplemented)
}

// HandleShowResourceClass handles GET /resource_classes/{name} requests.
//
// See: https://docs.openstack.org/api-ref/placement/#show-resource-class
func (s *Shim) HandleShowResourceClass(w http.ResponseWriter, r *http.Request) {
	if !s.config.Features.ResourceClasses {
		s.forward(w, r)
		return
	}
	if _, ok := requiredPathParam(w, r, "name"); !ok {
		return
	}
	http.Error(w, "not yet implemented", http.StatusNotImplemented)
}

// HandleUpdateResourceClass handles PUT /resource_classes/{name} requests.
//
// See: https://docs.openstack.org/api-ref/placement/#update-resource-class
func (s *Shim) HandleUpdateResourceClass(w http.ResponseWriter, r *http.Request) {
	if !s.config.Features.ResourceClasses {
		s.forward(w, r)
		return
	}
	if _, ok := requiredPathParam(w, r, "name"); !ok {
		return
	}
	http.Error(w, "not yet implemented", http.StatusNotImplemented)
}

// HandleDeleteResourceClass handles DELETE /resource_classes/{name} requests.
//
// See: https://docs.openstack.org/api-ref/placement/#delete-resource-class
func (s *Shim) HandleDeleteResourceClass(w http.ResponseWriter, r *http.Request) {
	if !s.config.Features.ResourceClasses {
		s.forward(w, r)
		return
	}
	if _, ok := requiredPathParam(w, r, "name"); !ok {
		return
	}
	http.Error(w, "not yet implemented", http.StatusNotImplemented)
}
