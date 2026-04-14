// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"
)

// HandleListTraits handles GET /traits requests.
//
// Returns a list of valid trait strings. Traits describe qualitative aspects
// of a resource provider (e.g. HW_CPU_X86_AVX2, STORAGE_DISK_SSD). The list
// includes both standard traits from the os-traits library and custom traits
// prefixed with CUSTOM_.
//
// Supports optional query parameters: name allows filtering by prefix
// (startswith:CUSTOM) or by an explicit list (in:TRAIT1,TRAIT2), and
// associated filters to only traits that are or are not associated with at
// least one resource provider.
func (s *Shim) HandleListTraits(w http.ResponseWriter, r *http.Request) {
	s.forward(w, r)
}

// HandleShowTrait handles GET /traits/{name} requests.
//
// Checks whether a trait with the given name exists. Returns 204 No Content
// (with no response body) if the trait is found, or 404 Not Found otherwise.
func (s *Shim) HandleShowTrait(w http.ResponseWriter, r *http.Request) {
	if _, ok := requiredPathParam(w, r, "name"); !ok {
		return
	}
	s.forward(w, r)
}

// HandleUpdateTrait handles PUT /traits/{name} requests.
//
// Creates a new custom trait. Only traits prefixed with CUSTOM_ may be
// created; standard traits are read-only. Returns 201 Created if the trait
// is newly inserted, or 204 No Content if it already exists. Returns 400
// Bad Request if the name does not carry the CUSTOM_ prefix.
func (s *Shim) HandleUpdateTrait(w http.ResponseWriter, r *http.Request) {
	if _, ok := requiredPathParam(w, r, "name"); !ok {
		return
	}
	s.forward(w, r)
}

// HandleDeleteTrait handles DELETE /traits/{name} requests.
//
// Deletes a custom trait. Standard traits (those without the CUSTOM_ prefix)
// cannot be deleted and will return 400 Bad Request. Returns 409 Conflict if
// the trait is still associated with any resource provider. Returns 404 if
// the trait does not exist. Returns 204 No Content on success.
func (s *Shim) HandleDeleteTrait(w http.ResponseWriter, r *http.Request) {
	if _, ok := requiredPathParam(w, r, "name"); !ok {
		return
	}
	s.forward(w, r)
}
