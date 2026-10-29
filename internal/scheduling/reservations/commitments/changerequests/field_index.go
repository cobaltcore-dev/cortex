// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package changerequests

import (
	"context"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// idxCRCRByUpsertedCR indexes CommittedResourceChangeRequests by each CR name in UpsertedCommitments.
// Used by crToChangeRequest to map a CR watch event back to its owning change request.
const idxCRCRByUpsertedCR = "status.upsertedCommitments.name"

// indexCRCRByUpsertedCR registers the field index on all clusters that serve CRCR objects.
// Called once from SetupWithManager; no deduplication guard needed.
func indexCRCRByUpsertedCR(ctx context.Context, mcl *multicluster.Client) error {
	return mcl.IndexField(ctx,
		&v1alpha1.CommittedResourceChangeRequest{},
		&v1alpha1.CommittedResourceChangeRequestList{},
		idxCRCRByUpsertedCR,
		func(obj client.Object) []string {
			tx, ok := obj.(*v1alpha1.CommittedResourceChangeRequest)
			if !ok {
				return nil
			}
			names := make([]string, 0, len(tx.Status.UpsertedCommitments))
			for _, ref := range tx.Status.UpsertedCommitments {
				if ref.Name != "" {
					names = append(names, ref.Name)
				}
			}
			return names
		},
	)
}
