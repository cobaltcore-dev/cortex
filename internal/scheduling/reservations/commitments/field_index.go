// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"errors"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const idxCommittedResourceByUUID = "spec.commitmentUUID"
const idxReservationByCommitmentUUID = "spec.committedResourceReservation.commitmentUUID"

// IndexFields registers field indexes required by the CommittedResource controller.
func IndexFields(ctx context.Context, mcl *multicluster.Client) error {
	log := logf.FromContext(ctx)
	log.Info("Setting up field indexes for the CommittedResource controller")
	if err := mcl.IndexField(ctx,
		&v1alpha1.CommittedResource{},
		&v1alpha1.CommittedResourceList{},
		idxCommittedResourceByUUID,
		func(obj client.Object) []string {
			cr, ok := obj.(*v1alpha1.CommittedResource)
			if !ok {
				log.Error(errors.New("unexpected type"), "expected CommittedResource", "object", obj)
				return nil
			}
			if cr.Spec.CommitmentUUID == "" {
				return nil
			}
			return []string{cr.Spec.CommitmentUUID}
		},
	); err != nil {
		log.Error(err, "failed to set up index for commitmentUUID")
		return err
	}
	if err := mcl.IndexField(ctx,
		&v1alpha1.Reservation{},
		&v1alpha1.ReservationList{},
		idxReservationByCommitmentUUID,
		func(obj client.Object) []string {
			res, ok := obj.(*v1alpha1.Reservation)
			if !ok {
				log.Error(errors.New("unexpected type"), "expected Reservation", "object", obj)
				return nil
			}
			if res.Spec.CommittedResourceReservation == nil || res.Spec.CommittedResourceReservation.CommitmentUUID == "" {
				return nil
			}
			return []string{res.Spec.CommittedResourceReservation.CommitmentUUID}
		},
	); err != nil {
		log.Error(err, "failed to set up index for reservation commitmentUUID")
		return err
	}
	log.Info("Successfully set up field indexes")
	return nil
}
