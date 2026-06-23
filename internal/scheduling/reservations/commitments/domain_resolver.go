// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"
	"sync"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/domains"
)

// DomainResolver resolves OpenStack domain IDs to their human-readable names.
// Implementations must be safe for concurrent use.
type DomainResolver interface {
	// ResolveDomainName returns the name of the domain with the given ID.
	// Returns an error if the domain cannot be found or the lookup fails.
	ResolveDomainName(ctx context.Context, domainID string) (string, error)
}

// keystoneDomainResolver resolves domain IDs via the Keystone identity API.
// Names are cached indefinitely — domain names are immutable for the lifetime
// of an OpenStack deployment, and the controller is a long-lived process.
type keystoneDomainResolver struct {
	sc    *gophercloud.ServiceClient
	mu    sync.RWMutex
	cache map[string]string // domainID → name
}

// newKeystoneDomainResolver creates a resolver backed by the given Keystone service client.
// The caller is responsible for authenticating the provider before passing sc here.
func newKeystoneDomainResolver(sc *gophercloud.ServiceClient) *keystoneDomainResolver {
	return &keystoneDomainResolver{
		sc:    sc,
		cache: make(map[string]string),
	}
}

// ResolveDomainName returns the domain name for domainID, fetching it from Keystone on
// first access and serving subsequent calls from the in-process cache.
func (r *keystoneDomainResolver) ResolveDomainName(ctx context.Context, domainID string) (string, error) {
	r.mu.RLock()
	if name, ok := r.cache[domainID]; ok {
		r.mu.RUnlock()
		return name, nil
	}
	r.mu.RUnlock()

	// Upgrade to write-lock. Re-check after acquiring the write-lock to avoid a
	// redundant Keystone call when two goroutines race on the same uncached ID.
	r.mu.Lock()
	defer r.mu.Unlock()
	if name, ok := r.cache[domainID]; ok {
		return name, nil
	}

	domain, err := domains.Get(ctx, r.sc, domainID).Extract()
	if err != nil {
		return "", fmt.Errorf("keystone: failed to resolve domain %q: %w", domainID, err)
	}
	r.cache[domainID] = domain.Name
	return domain.Name, nil
}
