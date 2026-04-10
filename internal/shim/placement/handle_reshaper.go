// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// HandlePostReshaper handles POST /reshaper requests.
//
// Atomically migrates resource provider inventories and associated allocations
// in a single transaction. This endpoint is used when a provider tree needs to
// be restructured — for example, moving inventory from a root provider into
// newly created child providers — without leaving allocations in an
// inconsistent state during the transition.
//
// The request body contains the complete set of inventories (keyed by
// resource provider UUID) and allocations (keyed by consumer UUID) that
// should exist after the operation. The Placement service validates all
// inputs atomically and applies them in a single database transaction.
// Returns 204 No Content on success. Returns 409 Conflict if any referenced
// resource provider does not exist or if inventory/allocation constraints
// would be violated. Available since microversion 1.30.
func (s *Shim) HandlePostReshaper(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)
	log.Info("placement request", "method", r.Method, "path", r.URL.Path)
	s.forward(w, r)
}
