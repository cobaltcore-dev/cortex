// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

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
// In passthrough mode, the request is forwarded to upstream placement. In
// hybrid mode, the shim returns the intersection (narrower range) of the
// upstream and local version configs. In crd mode, the response is served
// from the static versioning config alone.
//
// See: https://docs.openstack.org/api-ref/placement/#list-versions
func (s *Shim) HandleGetRoot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	switch s.featureModeFromConfOrHeader(r, s.config.Features.Root, s.config.Versioning != nil) {
	case FeatureModePassthrough:
		log.Info("forwarding GET / to upstream placement")
		s.forward(w, r)

	case FeatureModeHybrid:
		log.Info("handling GET / in hybrid mode (version intersection)")
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
			var upstream versionDocument
			if err := json.NewDecoder(resp.Body).Decode(&upstream); err != nil {
				log.Error(err, "failed to decode upstream version document")
				s.writeJSON(w, http.StatusOK, s.staticVersionDocument())
				return
			}
			merged := s.intersectVersions(upstream)
			s.writeJSON(w, http.StatusOK, merged)
		})

	case FeatureModeCRD:
		log.Info("handling GET / with static version document")
		s.writeJSON(w, http.StatusOK, s.staticVersionDocument())

	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
	}
}

func (s *Shim) staticVersionDocument() versionDocument {
	return versionDocument{
		Versions: []versionEntry{{
			ID:         s.config.Versioning.ID,
			MaxVersion: s.config.Versioning.MaxVersion,
			MinVersion: s.config.Versioning.MinVersion,
			Status:     s.config.Versioning.Status,
			Links:      []versionLink{{Rel: "self", Href: ""}},
		}},
	}
}

// intersectVersions computes the intersection of the upstream and local
// version ranges. The result uses the higher min and lower max, yielding
// the narrowest compatible window. When no upstream version entries exist
// or the ranges don't overlap, the local config is returned as-is.
func (s *Shim) intersectVersions(upstream versionDocument) versionDocument {
	if len(upstream.Versions) == 0 {
		return s.staticVersionDocument()
	}
	uv := upstream.Versions[0]
	localMin := s.config.Versioning.MinVersion
	localMax := s.config.Versioning.MaxVersion

	mergedMin := maxVersion(localMin, uv.MinVersion)
	mergedMax := minVersion(localMax, uv.MaxVersion)

	if compareVersions(mergedMin, mergedMax) > 0 {
		return s.staticVersionDocument()
	}
	return versionDocument{
		Versions: []versionEntry{{
			ID:         s.config.Versioning.ID,
			MaxVersion: mergedMax,
			MinVersion: mergedMin,
			Status:     s.config.Versioning.Status,
			Links:      []versionLink{{Rel: "self", Href: ""}},
		}},
	}
}

// compareVersions compares two dot-separated version strings numerically.
// Returns -1, 0, or 1.
func compareVersions(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	for i := 0; i < len(aParts) || i < len(bParts); i++ {
		var av, bv int
		if i < len(aParts) {
			if v, err := strconv.Atoi(aParts[i]); err == nil {
				av = v
			}
		}
		if i < len(bParts) {
			if v, err := strconv.Atoi(bParts[i]); err == nil {
				bv = v
			}
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}

func maxVersion(a, b string) string {
	if compareVersions(a, b) >= 0 {
		return a
	}
	return b
}

func minVersion(a, b string) string {
	if compareVersions(a, b) <= 0 {
		return a
	}
	return b
}
