// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package transactions

import (
	"context"
	"errors"
	"sync"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// idxCRTByUpsertedCR indexes CommittedResourceTransactions by each CR name in UpsertedCommitments.
// Used by crToTransaction to map a CR watch event back to its owning transaction.
const idxCRTByUpsertedCR = "status.upsertedCommitments.name"

var onceIndexCRTByUpsertedCR sync.Once

// indexCRTByUpsertedCR registers the field index on all clusters that serve CRT objects.
func indexCRTByUpsertedCR(ctx context.Context, mcl *multicluster.Client) (err error) {
	onceIndexCRTByUpsertedCR.Do(func() {
		log := ctrl.LoggerFrom(ctx)
		err = mcl.IndexField(ctx,
			&v1alpha1.CommittedResourceTransaction{},
			&v1alpha1.CommittedResourceTransactionList{},
			idxCRTByUpsertedCR,
			func(obj client.Object) []string {
				tx, ok := obj.(*v1alpha1.CommittedResourceTransaction)
				if !ok {
					log.Error(errors.New("unexpected type"), "expected CommittedResourceTransaction", "object", obj)
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
	})
	return err
}
