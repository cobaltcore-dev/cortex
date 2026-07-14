// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package clientcache

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// InformerSource provides, per object type, the informers the cache attaches
// to for eviction purposes. It is satisfied structurally e.g. by
// *multicluster.Client (via ClustersForGVK + cluster.GetCache().GetInformer),
// so that this package does not need to import pkg/multicluster.
type InformerSource interface {
	// GetInformersForKind returns all informers serving the GVK of the given
	// object. The cache attaches Add/Update event handlers to each informer to
	// evict overlay entries once the real object appears in the informer cache.
	GetInformersForKind(ctx context.Context, obj client.Object) ([]cache.Informer, error)
}
