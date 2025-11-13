// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"errors"
	"log/slog"
	"slices"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/openstack/placement"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
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
	lib.BaseStep[api.ExternalSchedulerRequest, FilterExternalCustomerStepOpts]
}

// Prefix-match the domain name for external customer domains and filter out hosts
// that are not intended for external customers.
func (s *FilterExternalCustomerStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.StepResult, error) {
	result := s.PrepareResult(request)
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
	var externalCustomerComputeHosts []string
	if _, err := s.DB.SelectTimed("scheduler-nova", &externalCustomerComputeHosts, `
        SELECT h.service_host
        FROM `+nova.Hypervisor{}.TableName()+` h
        JOIN `+placement.Trait{}.TableName()+` rpt
        ON h.id = rpt.resource_provider_uuid
        WHERE rpt.name = 'CUSTOM_EXTERNAL_CUSTOMER_SUPPORTED'`,
	); err != nil {
		return nil, err
	}
	lookupStr := strings.Join(externalCustomerComputeHosts, ",")
	for host := range result.Activations {
		if !strings.Contains(lookupStr, host) {
			continue
		}
		delete(result.Activations, host)
		traceLog.Debug("filtering host not intended for external customers", "host", host)
	}
	return result, nil
}
