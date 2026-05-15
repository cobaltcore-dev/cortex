// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"context"
	"log/slog"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

// Options for the KVM committed resource reservation weigher.
type KVMCommittedResourceReservationOpts struct {
	// Weight assigned to hosts that have a matching Reservation with free capacity.
	// Default: 1.0
	ReservationHostWeight *float64 `json:"reservationHostWeight,omitempty"`
	// Weight assigned to hosts without a matching Reservation.
	// Default: 0.1
	DefaultHostWeight *float64 `json:"defaultHostWeight,omitempty"`
}

func (o KVMCommittedResourceReservationOpts) Validate() error {
	return nil
}

func (o KVMCommittedResourceReservationOpts) GetReservationHostWeight() float64 {
	if o.ReservationHostWeight == nil {
		return 1.0
	}
	return *o.ReservationHostWeight
}

func (o KVMCommittedResourceReservationOpts) GetDefaultHostWeight() float64 {
	if o.DefaultHostWeight == nil {
		return 0.1
	}
	return *o.DefaultHostWeight
}

// KVMCommittedResourceReservationStep weighs hosts based on committed resource reservations.
// Hosts that have a ready Reservation matching the request's project and flavor group,
// with enough free memory capacity for the requested VM, receive a higher weight.
type KVMCommittedResourceReservationStep struct {
	lib.BaseWeigher[api.ExternalSchedulerRequest, KVMCommittedResourceReservationOpts]
}

// Run the weigher step.
// Hosts with a matching CommittedResourceReservation (project + flavor group + free capacity) get a higher weight.
func (s *KVMCommittedResourceReservationStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)

	projectID := request.Spec.Data.ProjectID
	az := request.Spec.Data.AvailabilityZone
	resourceGroup, err := request.Spec.Data.GetSchedulerHintStr(api.HintKeyResourceGroup)
	if err != nil || resourceGroup == "" {
		traceLog.Info("skipping committed resource reservation weigher: no resource group in request")
		return result, nil //nolint:nilerr
	}

	// Flavor memory in bytes for free-capacity check.
	flavorMemoryBytes := int64(request.Spec.Data.Flavor.Data.MemoryMB) * 1024 * 1024 //nolint:gosec

	var reservations v1alpha1.ReservationList
	if err := s.Client.List(context.Background(), &reservations); err != nil {
		return nil, err
	}

	// Collect hosts that have a matching reservation with sufficient free capacity.
	reservationHosts := make(map[string]bool)
	for i := range reservations.Items {
		res := &reservations.Items[i]
		if !res.IsReady() {
			continue
		}
		if res.Spec.Type != v1alpha1.ReservationTypeCommittedResource {
			continue
		}
		cr := res.Spec.CommittedResourceReservation
		if cr == nil {
			continue
		}
		if cr.ProjectID != projectID {
			continue
		}
		if cr.ResourceGroup != resourceGroup {
			continue
		}
		if res.Spec.AvailabilityZone != az {
			continue
		}
		if freeMemoryBytes(res) >= flavorMemoryBytes {
			reservationHosts[res.Status.Host] = true
			traceLog.Info("found committed resource reservation with free capacity",
				"reservation", res.Name,
				"host", res.Status.Host,
				"project", projectID,
				"resourceGroup", resourceGroup,
			)
		}
	}

	reservationWeight := s.Options.GetReservationHostWeight()
	defaultWeight := s.Options.GetDefaultHostWeight()

	for _, host := range request.Hosts {
		if reservationHosts[host.ComputeHost] {
			result.Activations[host.ComputeHost] = reservationWeight
		} else {
			result.Activations[host.ComputeHost] = defaultWeight
		}
	}

	return result, nil
}

// freeMemoryBytes returns the free memory in bytes for a committed resource reservation:
// total reserved memory minus the sum of memory already allocated to running VMs.
func freeMemoryBytes(res *v1alpha1.Reservation) int64 {
	total := res.Spec.Resources[hv1.ResourceMemory]
	var used resource.Quantity
	if res.Spec.CommittedResourceReservation != nil {
		for _, alloc := range res.Spec.CommittedResourceReservation.Allocations {
			if mem, ok := alloc.Resources[hv1.ResourceMemory]; ok {
				used.Add(mem)
			}
		}
	}
	return total.Value() - used.Value()
}

func init() {
	Index["kvm_committed_resource_reservation"] = func() NovaWeigher {
		return &KVMCommittedResourceReservationStep{}
	}
}
