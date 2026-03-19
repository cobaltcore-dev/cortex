// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package multicluster

import (
	"errors"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
)

// ResourceRouter determines which remote cluster a resource should be written to
// by matching the resource content against the cluster's labels.
type ResourceRouter interface {
	Match(obj any, labels map[string]string) (bool, error)
}

// HypervisorResourceRouter routes hypervisors to clusters based on availability zone.
type HypervisorResourceRouter struct{}

func (h HypervisorResourceRouter) Match(obj any, labels map[string]string) (bool, error) {
	var hv hv1.Hypervisor
	switch v := obj.(type) {
	case *hv1.Hypervisor:
		hv = *v
	case hv1.Hypervisor:
		hv = v
	default:
		return false, errors.New("object is not a Hypervisor")
	}
	availabilityZone, ok := labels["availability_zone"]
	if !ok {
		return false, errors.New("cluster does not have availability zone label")
	}
	hvAvailabilityZone, ok := hv.Labels[corev1.LabelTopologyZone]
	if !ok {
		return false, errors.New("hypervisor does not have availability zone label")
	}
	return hvAvailabilityZone == availabilityZone, nil
}

// ReservationsResourceRouter routes reservations to clusters based on availability zone.
type ReservationsResourceRouter struct{}

func (r ReservationsResourceRouter) Match(obj any, labels map[string]string) (bool, error) {
	var res v1alpha1.Reservation
	switch v := obj.(type) {
	case *v1alpha1.Reservation:
		res = *v
	case v1alpha1.Reservation:
		res = v
	default:
		return false, errors.New("object is not a Reservation")
	}
	availabilityZone, ok := labels["availability_zone"]
	if !ok {
		return false, errors.New("cluster does not have availability zone in spec")
	}
	reservationAvailabilityZone := res.Spec.AvailabilityZone
	if reservationAvailabilityZone == "" {
		return false, errors.New("reservation does not have availability zone in spec")
	}
	return reservationAvailabilityZone == availabilityZone, nil
}

// TODO: Add router for Decision CRD and reservations after their refactoring is done.
