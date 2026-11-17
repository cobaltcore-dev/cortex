// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"errors"
	"path"
	"slices"
	"strings"

	"github.com/go-logr/logr"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

// Trait marking hosts that are exclusively reserved for external customer domains.
const externalCustomerExclusiveTrait = "CUSTOM_EXTERNAL_CUSTOMER_EXCLUSIVE"

type FilterExternalCustomerStepOpts struct {
	CustomerDomainNamePrefixes []string `json:"domainNamePrefixes"`
	CustomerIgnoredDomainNames []string `json:"ignoredDomainNames"`
	// Allow-list of external customer domain names that may be scheduled on hosts
	// carrying the external customer exclusive trait. Each entry is a glob pattern
	// matched with path.Match, so exact names, "iaas-foo-*", or "*" are supported.
	// An empty or missing list blocks all external customer domains (fail-closed).
	CustomerAllowedDomainNames []string `json:"allowedDomainNames"`
}

func (opts FilterExternalCustomerStepOpts) Validate() error {
	if len(opts.CustomerDomainNamePrefixes) == 0 {
		return errors.New("don't configure this step without domainNamePrefixes")
	}
	// Fail fast on invalid allow-list patterns instead of silently never matching.
	for _, pattern := range opts.CustomerAllowedDomainNames {
		if _, err := path.Match(pattern, ""); err != nil {
			return errors.New("invalid allowedDomainNames pattern: " + pattern)
		}
	}
	return nil
}

type FilterExternalCustomerStep struct {
	lib.BaseFilter[compute.PlacementRequest, FilterExternalCustomerStepOpts]
}

// matchesExternalCustomerPrefix reports whether the domain name is an external
// customer domain, i.e. it matches one of the configured prefixes and is not
// explicitly ignored.
func (s *FilterExternalCustomerStep) matchesExternalCustomerPrefix(domainName string) bool {
	if domainName == "" {
		return false
	}
	if slices.Contains(s.Options.CustomerIgnoredDomainNames, domainName) {
		return false
	}
	for _, prefix := range s.Options.CustomerDomainNamePrefixes {
		if strings.HasPrefix(domainName, prefix) {
			return true
		}
	}
	return false
}

// isAllowed reports whether the external customer domain is on the allow-list.
func (s *FilterExternalCustomerStep) isAllowed(domainName string) bool {
	for _, pattern := range s.Options.CustomerAllowedDomainNames {
		if ok, err := path.Match(pattern, domainName); err == nil && ok {
			return true
		}
	}
	return false
}

// Restrict external customer domains to hosts carrying the external customer
// exclusive trait and, conversely, keep every other domain off those hosts.
//
// Failover reservation requests (reserve_for_failover, reuse_failover_reservation)
// carry the owning VM's domain, so they are subject to the same rules as a real
// placement: an external customer VM's failover slot may only be created/reused on
// exclusive hosts, and a regular VM's slot must avoid them.
func (s *FilterExternalCustomerStep) Run(traceLog logr.Logger, request compute.PlacementRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)

	// Capacity probes are synthetic requests with no real domain, so they must
	// not be subjected to external customer isolation: a probe measures the whole
	// fleet, including hosts exclusively reserved for external customers.
	if request.Intent == api.CapacityProbeIntent {
		return result, nil
	}

	isExternalCustomer := s.matchesExternalCustomerPrefix(request.DomainName)

	// External customer domains must be on the allow-list. An empty allow-list
	// blocks all external customer domains (fail-closed).
	if isExternalCustomer && !s.isAllowed(request.DomainName) {
		traceLog.V(1).Info("external customer domain not on allow-list, blocking placement", "domain", request.DomainName)
		result.Activations = map[string]float64{}
		return result, nil
	}

	exclusiveHosts, err := s.exclusiveHosts(result)
	if err != nil {
		traceLog.Error(err, "failed to list hypervisors")
		return nil, err
	}
	traceLog.V(1).Info("hosts reserved for external customers", "hosts", exclusiveHosts)

	for host := range result.Activations {
		_, isExclusive := exclusiveHosts[host]
		if isExternalCustomer == isExclusive {
			// External customers keep exclusive hosts; everyone else keeps the rest.
			continue
		}
		delete(result.Activations, host)
		traceLog.V(1).Info("filtering host", "host", host, "exclusive", isExclusive, "externalCustomer", isExternalCustomer)
	}
	return result, nil
}

// exclusiveHosts returns the set of candidate hosts carrying the external
// customer exclusive trait.
func (s *FilterExternalCustomerStep) exclusiveHosts(result *lib.FilterWeigherPipelineStepResult) (map[string]struct{}, error) {
	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(context.Background(), hvs); err != nil {
		return nil, err
	}
	exclusive := make(map[string]struct{})
	for _, hv := range hvs.Items {
		if _, ok := result.Activations[hv.Name]; !ok {
			continue
		}
		traits := slices.Concat(hv.Status.Traits, hv.Spec.CustomTraits)
		if slices.Contains(traits, externalCustomerExclusiveTrait) {
			exclusive[hv.Name] = struct{}{}
		}
	}
	return exclusive, nil
}

func init() {
	Index["filter_external_customer"] = func() NovaFilter { return &FilterExternalCustomerStep{} }
}
