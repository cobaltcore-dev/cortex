// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// HandleGetRoot handles GET / requests.
//
// Returns information about all known major versions of the Placement API,
// including the minimum and maximum supported microversions for each version.
// Currently only one major version (v1.0) exists. Each version entry includes
// its status (e.g. CURRENT), links for discovery, and the microversion range
// supported by the running service. Clients use this endpoint to discover API
// capabilities and negotiate microversions before making further requests.
func (s *Shim) HandleGetRoot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)
	log.Info("placement request", "method", r.Method, "path", r.URL.Path)
	s.forward(w, r)
}
