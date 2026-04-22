// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// versionDocument matches the OpenStack Placement API root response format.
type versionDocument struct {
	Versions []versionEntry `json:"versions"`
}

type versionEntry struct {
	ID         string        `json:"id"`
	MaxVersion string        `json:"max_version"`
	MinVersion string        `json:"min_version"`
	Status     string        `json:"status"`
	Links      []versionLink `json:"links"`
}

type versionLink struct {
	Rel  string `json:"rel"`
	Href string `json:"href"`
}

// HandleGetRoot handles GET / requests.
//
// Returns information about all known major versions of the Placement API,
// including the minimum and maximum supported microversions for each version.
// Currently only one major version (v1.0) exists. Each version entry includes
// its status (e.g. CURRENT), links for discovery, and the microversion range
// supported by the running service. Clients use this endpoint to discover API
// capabilities and negotiate microversions before making further requests.
//
// When features.enableRoot is true, the response is served from the static
// versioning config. When false, the request is forwarded to upstream placement.
//
// See: https://docs.openstack.org/api-ref/placement/#list-versions
func (s *Shim) HandleGetRoot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	if !s.config.Features.EnableRoot {
		log.Info("forwarding GET / to upstream placement")
		s.forward(w, r)
		return
	}

	log.Info("handling GET / with static version document")
	s.writeJSON(w, http.StatusOK, versionDocument{
		Versions: []versionEntry{{
			ID:         s.config.Versioning.ID,
			MaxVersion: s.config.Versioning.MaxVersion,
			MinVersion: s.config.Versioning.MinVersion,
			Status:     s.config.Versioning.Status,
			Links:      []versionLink{{Rel: "self", Href: ""}},
		}},
	})
}
