// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
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
func (s *FilterExternalCustomerStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)
	domainName, err := request.Spec.Data.GetSchedulerHintStr("domain_name")
	if err != nil {
		return nil, err
	}
	if slices.Contains(s.Options.CustomerIgnoredDomainNames, domainName) {
		traceLog.Debug("ignoring external customer domain", "domain", domainName)
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
		traceLog.Debug("domain does not match any external customer prefix", "domain", domainName)
		return result, nil
	}

	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(context.Background(), hvs); err != nil {
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
			continue
		}
		delete(result.Activations, host)
		traceLog.Info("filtering host not supporting external customers", "host", host)
	}
	return result, nil
}
