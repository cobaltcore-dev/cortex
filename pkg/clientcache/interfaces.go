// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package clientcache

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Client is the interface the CachingClient requires of its inner client.
// It extends client.Client with the informer access needed for overlay eviction.
// *multicluster.Client satisfies this interface.
type Client interface {
	client.Client
	// GetInformersForKind returns all informers serving the GVK of the given
	// object. The cache attaches Add/Update event handlers to each informer to
	// evict overlay entries once the real object appears in the informer cache.
	GetInformersForKind(ctx context.Context, obj client.Object) ([]cache.Informer, error)
}
