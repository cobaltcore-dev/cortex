// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"errors"
	"sync"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const idxCommittedResourceByUUID = "spec.commitmentUUID"
const idxCommittedResourceByProjectID = "spec.projectID"
const idxReservationByCommitmentUUID = "spec.committedResourceReservation.commitmentUUID"

// once guards ensure each field index is registered exactly once.
// Both CommittedResourceController and UsageReconciler call indexCommittedResourceByUUID;
// the underlying cache returns "indexer conflict" on double registration.
var (
	onceIndexCRByUUID          sync.Once
	onceIndexCRByProjectID     sync.Once
	onceIndexReservationByUUID sync.Once
)

// indexCommittedResourceByUUID registers the index used by UsageReconciler to look up
// CommittedResources by their CommitmentUUID.
func indexCommittedResourceByUUID(ctx context.Context, mcl *multicluster.Client) (err error) {
	onceIndexCRByUUID.Do(func() {
		log := logf.FromContext(ctx)
		err = mcl.IndexField(ctx,
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
	})
	return err
}

// indexCommittedResourceByProjectID registers the index used to look up CommittedResources
// by their project ID, avoiding full-cluster scans when filtering per project.
func indexCommittedResourceByProjectID(ctx context.Context, mcl *multicluster.Client) (err error) {
	onceIndexCRByProjectID.Do(func() {
		log := logf.FromContext(ctx)
		err = mcl.IndexField(ctx,
			&v1alpha1.CommittedResource{},
			&v1alpha1.CommittedResourceList{},
			idxCommittedResourceByProjectID,
			func(obj client.Object) []string {
				cr, ok := obj.(*v1alpha1.CommittedResource)
				if !ok {
					log.Error(errors.New("unexpected type"), "expected CommittedResource", "object", obj)
					return nil
				}
				if cr.Spec.ProjectID == "" {
					return nil
				}
				return []string{cr.Spec.ProjectID}
			},
		)
	})
	return err
}

// indexReservationByCommitmentUUID registers the index used by CommittedResourceController to
// look up child Reservations by their CommitmentUUID.
func indexReservationByCommitmentUUID(ctx context.Context, mcl *multicluster.Client) (err error) {
	onceIndexReservationByUUID.Do(func() {
		log := logf.FromContext(ctx)
		err = mcl.IndexField(ctx,
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
	})
	return err
}
