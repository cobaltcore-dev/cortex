// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

// requiredPathParam extracts a path parameter by name and verifies that it is
// non-empty. If the value is missing, it writes a 400 response and returns
// an empty string.
func requiredPathParam(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	v := r.PathValue(name)
	if v == "" {
		http.Error(w, fmt.Sprintf("missing path parameter: %s", name), http.StatusBadRequest)
		return "", false
	}
	return v, true
}

// requiredUUIDPathParam extracts a path parameter by name and verifies that it
// is a valid UUID. If the value is missing or not a valid UUID, it writes a
// 400 response and returns an empty string.
func requiredUUIDPathParam(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	v, ok := requiredPathParam(w, r, name)
	if !ok {
		return "", false
	}
	if err := uuid.Validate(v); err != nil {
		http.Error(w, fmt.Sprintf("invalid UUID in path parameter %s: %s", name, v), http.StatusBadRequest)
		return "", false
	}
	return v, true
}
