// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

type FilterExternalCustomerStepOpts struct {
	CustomerDomainNamePrefixes []string `json:"domainNamePrefixes"`
	CustomerIgnoredDomainNames []string `json:"ignoredDomainNames"`
}

func (opts FilterExternalCustomerStepOpts) Validate() error {
	if len(opts.CustomerDomainNamePrefixes) == 0 {
		return errors.New("don't configure this step without domainNamePrefixes")
	}
	return nil
}

type FilterExternalCustomerStep struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, FilterExternalCustomerStepOpts]
}

// Prefix-match the domain name for external customer domains and filter out hosts
// that are not intended for external customers.
func (s *FilterExternalCustomerStep) Run(ctx context.Context, traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)
	domainName, err := request.Spec.Data.GetSchedulerHintStr("domain_name")
	if err != nil {
		traceLog.Error("failed to get domain_name scheduler hint", "error", err)
		return nil, err
	}
	if slices.Contains(s.Options.CustomerIgnoredDomainNames, domainName) {
		traceLog.Info("domain is no external customer domain, skipping filter", "domain", domainName)
		return result, nil
	}
	found := false
	for _, prefix := range s.Options.CustomerDomainNamePrefixes {
		if strings.HasPrefix(domainName, prefix) {
			found = true
			break
		}
	}
	if !found {
		traceLog.Info("domain does not match any external customer prefix -- skipping filter", "domain", domainName)
		return result, nil
	}

	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(ctx, hvs); err != nil {
		traceLog.Error("failed to list hypervisors", "error", err)
		return nil, err
	}
	hvsWithTrait := make(map[string]struct{})
	for _, hv := range hvs.Items {
		traits := hv.Status.Traits
		traits = append(traits, hv.Spec.CustomTraits...)
		if !slices.Contains(traits, "CUSTOM_EXTERNAL_CUSTOMER_SUPPORTED") {
			continue
		}
		hvsWithTrait[hv.Name] = struct{}{}
	}

	traceLog.Info("hosts supporting external customers", "hosts", hvsWithTrait)
	for host := range result.Activations {
		if _, ok := hvsWithTrait[host]; ok {
			traceLog.Info("host supports external customers, keeping", "host", host)
			continue
		}
		delete(result.Activations, host)
		traceLog.Info("filtering host not supporting external customers", "host", host)
	}
	return result, nil
}

func init() {
	Index["filter_external_customer"] = func() NovaFilter { return &FilterExternalCustomerStep{} }
}
