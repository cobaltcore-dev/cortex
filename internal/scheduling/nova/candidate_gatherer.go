// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"fmt"
	"strings"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CandidateGatherer is an interface for gathering placement candidates
// for a given Nova scheduling request.
type CandidateGatherer interface {
	// Gather all placement candidates and mutate the request accordingly.
	MutateWithAllCandidates(ctx context.Context, request *api.ExternalSchedulerRequest) error
}

// candidateGatherer is the default implementation of CandidateGatherer
// for Nova scheduling requests.
type candidateGatherer struct{ client.Client }

// MutateWithAllCandidates gathers all placement candidates and mutates
// the request accordingly.
func (g *candidateGatherer) MutateWithAllCandidates(ctx context.Context, request *api.ExternalSchedulerRequest) error {
	// Currently we can only get candidates for kvm placements.
	hvType, ok := request.Spec.Data.Flavor.Data.ExtraSpecs["capabilities:hypervisor_type"]
	if !ok {
		return fmt.Errorf(
			"missing hypervisor_type in flavor extra specs: %v",
			request.Spec.Data.Flavor.Data.ExtraSpecs,
		)
	}
	switch strings.ToLower(hvType) {
	case "qemu", "ch":
		// Supported hypervisor type.
	default:
		// Unsupported hypervisor type, do nothing.
		return fmt.Errorf(
			"cannot gather all placement candidates for hypervisor type %q",
			request.Spec.Data.Flavor.Data.ExtraSpecs["capabilities:hypervisor_type"],
		)
	}

	// List all kvm hypervisors.
	hypervisorList := &hv1.HypervisorList{}
	if err := g.List(ctx, hypervisorList); err != nil {
		return err
	}
	hosts := make([]api.ExternalSchedulerHost, 0, len(hypervisorList.Items))
	weights := make(map[string]float64, len(hypervisorList.Items))
	for _, hv := range hypervisorList.Items {
		host := api.ExternalSchedulerHost{
			// For KVM hosts, compute host name and hypervisor hostname is identical.
			ComputeHost:        hv.Name,
			HypervisorHostname: hv.Name,
		}
		hosts = append(hosts, host)
		weights[host.ComputeHost] = 0.0 // Default weight.
	}

	// Mutate the request with all gathered hosts and weights.
	request.Hosts = hosts
	request.Weights = weights
	return nil
}
