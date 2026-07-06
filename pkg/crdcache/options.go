// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package crdcache

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DefaultTTL is the default time-to-live for a cache entry when no TTL is configured.
const DefaultTTL = 2 * time.Minute

// Config is the top-level values.yaml-configurable struct for CRDCache.
type Config struct {
	CRDCache CRDCacheSettings `json:"crdCache"`
}

// CRDCacheSettings holds the per-cache settings nested under the crdCache key.
type CRDCacheSettings struct {
	// TTL is the maximum time an assumed entry stays in the cache before being
	// evicted by the TTL timer. Defaults to DefaultTTL when zero.
	TTL Duration `json:"ttl"`
	// GVKs is the list of GroupVersionKinds whose objects should be cached.
	// When empty, all objects are cached.
	GVKs []GVK `json:"gvks,omitempty"`
}

// GVK is a GroupVersionKind expressed as "group/version/kind" that unmarshals from a plain string.
type GVK schema.GroupVersionKind

func (g *GVK) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	parts := strings.SplitN(s, "/", 3)
	if len(parts) != 3 {
		return fmt.Errorf("crdcache: GVK %q must be in group/version/kind format", s)
	}
	g.Group, g.Version, g.Kind = parts[0], parts[1], parts[2]
	return nil
}

// ShouldCacheFunc returns a predicate that returns true for objects whose GVK
// is in the configured list. When no GVKs are configured it returns nil (cache everything).
// The provided scheme is used to resolve the GVK of each object.
func (s CRDCacheSettings) ShouldCacheFunc(scheme *runtime.Scheme) func(client.Object) bool {
	if len(s.GVKs) == 0 {
		return nil
	}
	allowed := make(map[schema.GroupVersionKind]struct{}, len(s.GVKs))
	for _, g := range s.GVKs {
		allowed[schema.GroupVersionKind(g)] = struct{}{}
	}
	return func(obj client.Object) bool {
		gvks, _, err := scheme.ObjectKinds(obj)
		if err != nil {
			return false
		}
		for _, gvk := range gvks {
			if _, ok := allowed[gvk]; ok {
				return true
			}
		}
		return false
	}
}

// Duration is a time.Duration that marshals/unmarshals as a Go duration string (e.g. "2m30s").
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

// Options configures CRDCache behaviour.
type Options struct {
	// Config holds the externally-configurable settings (e.g. TTL from values.yaml).
	Config Config

	// ShouldCache, when non-nil, is called for every object written via CachingClient.
	// Return false to skip caching the object (it is still written to the API server).
	// When nil, all objects are cached.
	ShouldCache func(obj client.Object) bool
}

func (o Options) ttl() time.Duration {
	if o.Config.CRDCache.TTL.Duration == 0 {
		return DefaultTTL
	}
	return o.Config.CRDCache.TTL.Duration
}
