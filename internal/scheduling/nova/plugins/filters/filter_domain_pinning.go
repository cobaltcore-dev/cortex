// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"errors"

	"github.com/go-logr/logr"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type FilterDomainPinningStepOpts struct {
	// Maps a domain name to the set of hypervisors (by their Hypervisor CRD
	// name, which equals the compute host name) that the domain may be scheduled
	// on. A domain present in this map is restricted to exactly its hypervisors.
	// Hypervisors listed here are isolated: no domain outside the map may use
	// them.
	DomainHypervisors map[string][]string `json:"domainHypervisors"`
}

func (opts FilterDomainPinningStepOpts) Validate() error {
	// An empty mapping is allowed: the step is then a noop. This lets the step
	// stay in the pipeline unconditionally and be activated via config only.
	for domain, hosts := range opts.DomainHypervisors {
		if len(hosts) == 0 {
			return errors.New("domainHypervisors must not contain empty host lists: " + domain)
		}
	}
	return nil
}

type FilterDomainPinningStep struct {
	lib.BaseFilter[compute.PlacementRequest, FilterDomainPinningStepOpts]
}

// Pin specific domains to a dedicated set of hypervisors and keep those
// hypervisors isolated from all other domains.
//
// Failover reservation requests (reserve_for_failover, reuse_failover_reservation)
// carry the owning VM's domain, so a pinned domain's failover slot is confined to
// its dedicated hypervisors and every other domain's slot avoids them.
func (s *FilterDomainPinningStep) Run(traceLog logr.Logger, request compute.PlacementRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)

	// TODO: a capacity probe is a synthetic request with no real domain. Until it
	// carries the domain it probes for, it is treated as an unpinned domain and
	// thus conservatively excludes all pinned hypervisors. A proper implementation
	// would probe per-domain capacity.

	pinnedHosts, isPinnedDomain := s.Options.DomainHypervisors[request.DomainName]
	if isPinnedDomain {
		// Restrict the domain to exactly its configured hypervisors.
		allowed := make(map[string]struct{}, len(pinnedHosts))
		for _, host := range pinnedHosts {
			allowed[host] = struct{}{}
		}
		for host := range result.Activations {
			if _, ok := allowed[host]; ok {
				continue
			}
			delete(result.Activations, host)
			traceLog.V(1).Info("filtering host not pinned to domain", "host", host, "domain", request.DomainName)
		}
		return result, nil
	}

	// Any other domain must not land on a hypervisor pinned to some domain.
	reserved := s.reservedHosts()
	for host := range result.Activations {
		if _, ok := reserved[host]; !ok {
			continue
		}
		delete(result.Activations, host)
		traceLog.V(1).Info("filtering host reserved for a pinned domain", "host", host, "domain", request.DomainName)
	}
	return result, nil
}

// reservedHosts returns the set of all hypervisors pinned to any domain.
func (s *FilterDomainPinningStep) reservedHosts() map[string]struct{} {
	reserved := make(map[string]struct{})
	for _, hosts := range s.Options.DomainHypervisors {
		for _, host := range hosts {
			reserved[host] = struct{}{}
		}
	}
	return reserved
}

func init() {
	Index["filter_domain_pinning"] = func() NovaFilter { return &FilterDomainPinningStep{} }
}
