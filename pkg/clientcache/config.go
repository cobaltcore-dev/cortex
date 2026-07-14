// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package clientcache

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Config configures the transparent in-process overlay cache.
type Config struct {
	// GVKs the cache should overlay, formatted as "<group>/<version>/<Kind>".
	// Calls for GVKs not listed here are passed through unchanged.
	GVKs []string `json:"gvks"`
	// TTL is the maximum lifetime of an overlay entry before it is evicted by
	// the background cleanup goroutine, guarding against entries that never
	// appear in the informer (e.g. after a crash). Defaults to 2m when zero.
	TTL metav1.Duration `json:"ttl,omitempty"`
}

// RootConfig is the top-level config key for the client cache.
type RootConfig struct {
	ClientCache Config `json:"clientcache"`
}
