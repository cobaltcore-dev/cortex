// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package crdcache

import (
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DefaultTTL is the default time-to-live for a cache entry when no TTL is configured.
const DefaultTTL = 2 * time.Minute

// Options configures CRDCache behaviour.
type Options struct {
	// TTL is the maximum time an assumed entry stays in the cache before being
	// evicted by the TTL timer. Defaults to DefaultTTL when zero.
	TTL time.Duration

	// ShouldCache, when non-nil, is called for every object written via CachingClient.
	// Return false to skip caching the object (it is still written to the API server).
	// When nil, all objects are cached.
	ShouldCache func(obj client.Object) bool
}

func (o Options) ttl() time.Duration {
	if o.TTL == 0 {
		return DefaultTTL
	}
	return o.TTL
}
