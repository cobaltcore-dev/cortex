// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package multicluster

import (
	"errors"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// DefaultResourceRouters defines all mappings of GroupVersionKinds to RRs
// for the multicluster client that cortex supports by default. This is used to
// route resources to the correct cluster in a multicluster setup.
var DefaultResourceRouters = map[schema.GroupVersionKind]ResourceRouter{
	{Group: "kvm.cloud.sap", Version: "v1", Kind: "Hypervisor"}:       HypervisorResourceRouter{},
	{Group: "cortex.cloud", Version: "v1alpha1", Kind: "Reservation"}: ReservationsResourceRouter{},
	{Group: "cortex.cloud", Version: "v1alpha1", Kind: "History"}:     HistoryResourceRouter{},
}

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
		if v == nil {
			return false, errors.New("object is nil")
		}
		hv = *v
	case hv1.Hypervisor:
		hv = v
	default:
		return false, errors.New("object is not a Hypervisor")
	}
	availabilityZone, ok := labels["availabilityZone"]
	if !ok {
		return false, errors.New("cluster does not have availabilityZone label")
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
		if v == nil {
			return false, errors.New("object is nil")
		}
		res = *v
	case v1alpha1.Reservation:
		res = v
	default:
		return false, errors.New("object is not a Reservation")
	}
	availabilityZone, ok := labels["availabilityZone"]
	if !ok {
		return false, errors.New("cluster does not have availability zone label")
	}
	reservationAvailabilityZone := res.Spec.AvailabilityZone
	if reservationAvailabilityZone == "" {
		return false, errors.New("reservation does not have availability zone in spec")
	}
	return reservationAvailabilityZone == availabilityZone, nil
}

// HistoryResourceRouter routes histories to clusters based on availability zone.
type HistoryResourceRouter struct{}

func (h HistoryResourceRouter) Match(obj any, labels map[string]string) (bool, error) {
	var hist v1alpha1.History

	switch v := obj.(type) {
	case *v1alpha1.History:
		if v == nil {
			return false, errors.New("object is nil")
		}
		hist = *v
	case v1alpha1.History:
		hist = v
	default:
		return false, errors.New("object is not a History")
	}
	availabilityZone, ok := labels["availabilityZone"]
	if !ok {
		return false, errors.New("cluster does not have availabilityZone label")
	}
	if hist.Spec.AvailabilityZone == nil || *hist.Spec.AvailabilityZone == "" {
		return false, errors.New("history does not have availability zone in spec")
	}
	return *hist.Spec.AvailabilityZone == availabilityZone, nil
}
