// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"fmt"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CandidateGatherer is an interface for gathering placement candidates
// for a given Nova scheduling request.
type CandidateGatherer interface {
	// Gather all placement candidates and mutate the request accordingly.
	MutateWithAllCandidates(ctx context.Context, request *compute.SchedulerRequest) error
}

// candidateGatherer is the default implementation of CandidateGatherer
// for Nova scheduling requests.
type candidateGatherer struct{ client.Client }

// MutateWithAllCandidates gathers all placement candidates and mutates
// the request accordingly.
func (g *candidateGatherer) MutateWithAllCandidates(ctx context.Context, request *compute.SchedulerRequest) error {
	// Currently we can only get candidates for kvm placements.
	if request.HypervisorFamily != compute.HypervisorFamilyKVM {
		return fmt.Errorf(
			"cannot gather all placement candidates for hypervisor family %q",
			request.HypervisorFamily,
		)
	}

	// List all kvm hypervisors.
	hypervisorList := &hv1.HypervisorList{}
	if err := g.List(ctx, hypervisorList); err != nil {
		return err
	}
	hosts := make([]compute.Host, 0, len(hypervisorList.Items))
	weights := make(map[string]float64, len(hypervisorList.Items))
	for _, hv := range hypervisorList.Items {
		host := compute.Host{
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
