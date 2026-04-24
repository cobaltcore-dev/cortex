// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"fmt"
	"net/http"
)

// HandleListResourceClasses handles GET /resource_classes requests.
//
// Returns the complete list of all resource classes, including both standard
// classes (e.g. VCPU, MEMORY_MB, DISK_GB, PCI_DEVICE, SRIOV_NET_VF) and
// deployer-defined custom classes prefixed with CUSTOM_. Resource classes
// categorize the types of resources that resource providers can offer as
// inventory. Available since microversion 1.2.
func (s *Shim) HandleListResourceClasses(w http.ResponseWriter, r *http.Request) {
	switch s.config.Features.ResourceClasses.orDefault() {
	case FeatureModePassthrough:
		s.forward(w, r)
	case FeatureModeHybrid, FeatureModeCRD:
		http.Error(w, fmt.Sprintf("%s mode is not yet implemented for this endpoint", s.config.Features.ResourceClasses), http.StatusNotImplemented)
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
	}
}

// HandleCreateResourceClass handles POST /resource_classes requests.
//
// Creates a new custom resource class. The name must be prefixed with CUSTOM_
// to distinguish it from standard resource classes. Returns 201 Created with
// a Location header on success. Returns 400 Bad Request if the CUSTOM_ prefix
// is missing, and 409 Conflict if a class with the same name already exists.
// Available since microversion 1.2.
func (s *Shim) HandleCreateResourceClass(w http.ResponseWriter, r *http.Request) {
	switch s.config.Features.ResourceClasses.orDefault() {
	case FeatureModePassthrough:
		s.forward(w, r)
	case FeatureModeHybrid, FeatureModeCRD:
		http.Error(w, fmt.Sprintf("%s mode is not yet implemented for this endpoint", s.config.Features.ResourceClasses), http.StatusNotImplemented)
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
	}
}

// HandleShowResourceClass handles GET /resource_classes/{name} requests.
//
// Returns a representation of a single resource class identified by name.
// This can be used to verify the existence of a resource class. Returns 404
// if the class does not exist. Available since microversion 1.2.
func (s *Shim) HandleShowResourceClass(w http.ResponseWriter, r *http.Request) {
	if _, ok := requiredPathParam(w, r, "name"); !ok {
		return
	}
	switch s.config.Features.ResourceClasses.orDefault() {
	case FeatureModePassthrough:
		s.forward(w, r)
	case FeatureModeHybrid, FeatureModeCRD:
		http.Error(w, fmt.Sprintf("%s mode is not yet implemented for this endpoint", s.config.Features.ResourceClasses), http.StatusNotImplemented)
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
	}
}

// HandleUpdateResourceClass handles PUT /resource_classes/{name} requests.
//
// Behavior differs by microversion. Since microversion 1.7, this endpoint
// creates or validates the existence of a single resource class: it returns
// 201 Created for a new class or 204 No Content if the class already exists.
// The name must carry the CUSTOM_ prefix. In earlier versions (1.2-1.6), the
// endpoint allowed renaming a class via a request body, but this usage is
// discouraged. Returns 400 Bad Request if the CUSTOM_ prefix is missing.
func (s *Shim) HandleUpdateResourceClass(w http.ResponseWriter, r *http.Request) {
	if _, ok := requiredPathParam(w, r, "name"); !ok {
		return
	}
	switch s.config.Features.ResourceClasses.orDefault() {
	case FeatureModePassthrough:
		s.forward(w, r)
	case FeatureModeHybrid, FeatureModeCRD:
		http.Error(w, fmt.Sprintf("%s mode is not yet implemented for this endpoint", s.config.Features.ResourceClasses), http.StatusNotImplemented)
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
	}
}

// HandleDeleteResourceClass handles DELETE /resource_classes/{name} requests.
//
// Deletes a custom resource class. Only custom classes (prefixed with CUSTOM_)
// may be deleted; attempting to delete a standard class returns 400 Bad
// Request. Returns 409 Conflict if any resource provider has inventory of this
// class, and 404 if the class does not exist. Returns 204 No Content on
// success. Available since microversion 1.2.
func (s *Shim) HandleDeleteResourceClass(w http.ResponseWriter, r *http.Request) {
	if _, ok := requiredPathParam(w, r, "name"); !ok {
		return
	}
	switch s.config.Features.ResourceClasses.orDefault() {
	case FeatureModePassthrough:
		s.forward(w, r)
	case FeatureModeHybrid, FeatureModeCRD:
		http.Error(w, fmt.Sprintf("%s mode is not yet implemented for this endpoint", s.config.Features.ResourceClasses), http.StatusNotImplemented)
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
	}
}
