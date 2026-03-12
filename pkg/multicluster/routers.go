// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package multicluster

import (
	"errors"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

// ResourceRouter determines which remote cluster a resource should be written to
// by matching the resource content against the cluster's labels.
type ResourceRouter interface {
	Match(obj any, labels map[string]string) (bool, error)
}

// HypervisorResourceRouter routes hypervisors to clusters based on availability zone.
type HypervisorResourceRouter struct{}

func (h HypervisorResourceRouter) Match(obj any, labels map[string]string) (bool, error) {
	hv, ok := obj.(hv1.Hypervisor)
	if !ok {
		return false, errors.New("object is not a Hypervisor")
	}
	az, ok := labels["az"]
	if !ok {
		return false, errors.New("cluster does not have availability zone label")
	}
	hvAZ, ok := hv.Labels["topology.kubernetes.io/zone"]
	if !ok {
		return false, errors.New("hypervisor does not have availability zone label")
	}
	return hvAZ == az, nil
}

// TODO: Add router for Decision CRD and reservations after their refactoring is done.
