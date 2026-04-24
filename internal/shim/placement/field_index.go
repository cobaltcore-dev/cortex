// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"errors"

	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// idxHypervisorOpenStackId is the name of the index for looking up
	// hypervisors by their status.hypervisorId field, which represents the ID
	// of the hypervisor in OpenStack. This also corresponds to the uuid of the
	// resource provider in OpenStack Placement.
	idxHypervisorOpenStackId = "status.hypervisorId"
	// idxHypervisorKubernetesId is the name of the index for looking up
	// hypervisors by their uid in Kubernetes.
	idxHypervisorKubernetesId = "metadata.uid"
	// idxHypervisorName is the name of the index for looking up hypervisors
	// by their metadata.name field, which represents the name of the hypervisor
	// in Kubernetes.
	idxHypervisorName = "metadata.name"
)

// IndexFields indexes all fields that are needed by the shim to quickly
// look up objects from the controller-runtime cache.
// Both the singular object type and the list type are indexed because the
// multicluster client routes each based on its own GVK.
func IndexFields(ctx context.Context, mcl *multicluster.Client) error {
	log := logf.FromContext(ctx)
	log.Info("Setting up field indexes for the multicluster client")
	h, hl := &hv1.Hypervisor{}, &hv1.HypervisorList{}

	if err := mcl.IndexField(ctx, h, hl, idxHypervisorOpenStackId, func(obj client.Object) []string {
		hv, ok := obj.(*hv1.Hypervisor)
		if !ok {
			log.Error(errors.New("unexpected type"), "object", obj)
			return nil
		}
		if hv.Status.HypervisorID == "" {
			return nil
		}
		return []string{hv.Status.HypervisorID}
	}); err != nil {
		log.Error(err, "failed to set up index for hypervisorId")
		return err
	}
	log.Info("Successfully set up index for hypervisor OpenStack ID")

	if err := mcl.IndexField(ctx, h, hl, idxHypervisorKubernetesId, func(obj client.Object) []string {
		hv, ok := obj.(*hv1.Hypervisor)
		if !ok {
			log.Error(errors.New("unexpected type"), "object", obj)
			return nil
		}
		return []string{string(hv.UID)}
	}); err != nil {
		log.Error(err, "failed to set up index for hypervisor uid")
		return err
	}
	log.Info("Successfully set up index for hypervisor Kubernetes UID")

	if err := mcl.IndexField(ctx, h, hl, idxHypervisorName, func(obj client.Object) []string {
		hv, ok := obj.(*hv1.Hypervisor)
		if !ok {
			log.Error(errors.New("unexpected type"), "object", obj)
			return nil
		}
		return []string{hv.Name}
	}); err != nil {
		log.Error(err, "failed to set up index for hypervisor name")
		return err
	}
	log.Info("Successfully set up index for hypervisor name")

	return nil
}
