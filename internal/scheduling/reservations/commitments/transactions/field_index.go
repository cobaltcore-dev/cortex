// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package transactions

import (
	"context"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// idxCRTByUpsertedCR indexes CommittedResourceTransactions by each CR name in UpsertedCommitments.
// Used by crToTransaction to map a CR watch event back to its owning transaction.
const idxCRTByUpsertedCR = "status.upsertedCommitments.name"

// indexCRTByUpsertedCR registers the field index on all clusters that serve CRT objects.
// Called once from SetupWithManager; no deduplication guard needed.
func indexCRTByUpsertedCR(ctx context.Context, mcl *multicluster.Client) error {
	return mcl.IndexField(ctx,
		&v1alpha1.CommittedResourceTransaction{},
		&v1alpha1.CommittedResourceTransactionList{},
		idxCRTByUpsertedCR,
		func(obj client.Object) []string {
			tx, ok := obj.(*v1alpha1.CommittedResourceTransaction)
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
