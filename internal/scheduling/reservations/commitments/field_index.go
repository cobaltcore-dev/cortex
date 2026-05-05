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

// indexCommittedResourceByUUID registers the index used by UsageReconciler to look up
// CommittedResources by their CommitmentUUID.
func indexCommittedResourceByUUID(ctx context.Context, mcl *multicluster.Client) error {
	log := logf.FromContext(ctx)
	return mcl.IndexField(ctx,
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
	)
}

// indexReservationByCommitmentUUID registers the index used by CommittedResourceController to
// look up child Reservations by their CommitmentUUID.
func indexReservationByCommitmentUUID(ctx context.Context, mcl *multicluster.Client) error {
	log := logf.FromContext(ctx)
	return mcl.IndexField(ctx,
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
	)
}
