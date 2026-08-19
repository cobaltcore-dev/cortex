// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package multicluster

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/cobaltcore-dev/cortex/pkg/pendingcache"
)

// A remote cluster with routing labels used to match resources to clusters.
type remoteCluster struct {
	cluster cluster.Cluster
	labels  map[string]string
}

type Client struct {
	// ResourceRouters determine which cluster a resource should be written to
	// when multiple clusters serve the same GVK.
	ResourceRouters map[schema.GroupVersionKind]ResourceRouter

	// The cluster in which cortex is deployed.
	HomeCluster cluster.Cluster
	// The REST config for the home cluster in which cortex is deployed.
	HomeRestConfig *rest.Config
	// The scheme for the home cluster in which cortex is deployed.
	// This scheme should include all types used in the remote clusters.
	HomeScheme *runtime.Scheme

	// Optional monitor for Prometheus metrics. A nil Monitor causes recording
	// to be skipped, so the client can be used without wiring metrics.
	Monitor Monitor

	// Remote clusters to use by resource type. Multiple clusters can serve
	// the same GVK (e.g. one per availability zone).
	remoteClusters map[schema.GroupVersionKind][]remoteCluster
	// Mutex to protect access to remoteClusters.
	remoteClustersMu sync.RWMutex

	// GVKs explicitly configured for the home cluster.
	homeGVKs map[schema.GroupVersionKind]bool

	// cacheConf configures the optional in-process overlay cache. When
	// Enabled, each home/remote cluster is wrapped so its GetClient/
	// GetFieldIndexer route through a per-cluster overlay. Set in InitFromConf.
	cacheConf pendingcache.Config
}

// Helper function to initialize a new multicluster client during service startup,
// using the conf module provided by cortex.
func (c *Client) InitFromConf(ctx context.Context, mgr ctrl.Manager, conf ClientConfig) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("initializing multicluster client with config", "config", conf)
	c.cacheConf = conf.PendingCache
	// Map the formatted gvk from the config to the actual gvk object so that we
	// can look up the right cluster for a given API server override.
	gvksByConfStr := make(map[string]schema.GroupVersionKind)
	for gvk := range c.HomeScheme.AllKnownTypes() {
		formatted := gvk.GroupVersion().String() + "/" + gvk.Kind
		gvksByConfStr[formatted] = gvk
	}
	for gvkStr := range gvksByConfStr {
		log.Info("scheme gvk registered", "gvk", gvkStr)
	}
	// Parse home GVKs.
	c.homeGVKs = make(map[schema.GroupVersionKind]bool)
	for _, gvkStr := range conf.APIServers.Home.GVKs {
		gvk, ok := gvksByConfStr[gvkStr]
		if !ok {
			return errors.New("no gvk registered for home " + gvkStr)
		}
		log.Info("registering home gvk", "gvk", gvk)
		c.homeGVKs[gvk] = true
	}
	// Parse remote apiserver configs.
	for _, remote := range conf.APIServers.Remotes {
		var resolvedGVKs []schema.GroupVersionKind
		for _, gvkStr := range remote.GVKs {
			gvk, ok := gvksByConfStr[gvkStr]
			if !ok {
				return errors.New("no gvk registered for remote apiserver " + gvkStr)
			}
			resolvedGVKs = append(resolvedGVKs, gvk)
		}
		cl, overlay, err := c.AddRemote(ctx, remote.Host, remote.CACert, remote.InsecureSkipTLSVerify, remote.Labels, resolvedGVKs...)
		if err != nil {
			return err
		}
		// Add the raw inner cluster so its informers/caches start (as today).
		if err := mgr.Add(cl); err != nil {
			return err
		}
		// When caching is enabled, also add the overlay's lifecycle Runnable
		// (eviction handlers + TTL cleanup). It does not re-Start the cluster.
		if overlay != nil {
			if err := mgr.Add(overlay); err != nil {
				return err
			}
		}
	}
	// When caching is enabled, wrap the home cluster the same way. The manager
	// already owns the home cluster's lifecycle, so we only add the overlay
	// Runnable and must NOT re-Start the inner home cluster.
	if c.cacheConf.Enabled && c.HomeCluster != nil {
		wrapped, overlay, err := pendingcache.WrapCluster(c.HomeCluster, c.cacheConf)
		if err != nil {
			return err
		}
		c.HomeCluster = wrapped
		if err := mgr.Add(overlay); err != nil {
			return err
		}
	}
	return nil
}

// Add a remote cluster which uses the same REST config as the home cluster,
// but a different host, for the given resource gvks.
//
// If insecureSkipTLSVerify is true, the remote apiserver's TLS certificate
// is not verified and caCert is ignored. This is useful for apiservers whose
// CA certificate rotates frequently and does not chain to a stable root.
//
// This can be used when the remote cluster accepts the home cluster's service
// account tokens. See the kubernetes documentation on structured auth to
// learn more about jwt-based authentication across clusters.
// AddRemote returns the raw inner cluster.Cluster (which the caller must add to
// the manager so its informers/caches start) and, when caching is enabled, the
// overlay's lifecycle Runnable (which the caller must also add to the manager).
// The overlay Runnable is nil when caching is disabled. The wrapped cluster (or
// the raw cluster when caching is disabled) is stored in remoteClusters so all
// routing goes through the per-cluster overlay.
func (c *Client) AddRemote(ctx context.Context, host, caCert string, insecureSkipTLSVerify bool, labels map[string]string, gvks ...schema.GroupVersionKind) (cluster.Cluster, manager.Runnable, error) {
	log := ctrl.LoggerFrom(ctx)
	homeRestConfig := *c.HomeRestConfig
	restConfigCopy := homeRestConfig
	restConfigCopy.Host = host
	if insecureSkipTLSVerify {
		// Insecure and CAData are mutually exclusive in client-go's TLS validation.
		restConfigCopy.CAData = nil
		restConfigCopy.CAFile = ""
		restConfigCopy.Insecure = true
	} else {
		restConfigCopy.CAData = []byte(caCert)
	}
	cl, err := cluster.New(&restConfigCopy, func(o *cluster.Options) {
		o.Scheme = c.HomeScheme
		o.Logger = ctrl.LoggerFrom(ctx).WithValues("host", host)
	})
	if err != nil {
		return nil, nil, err
	}
	// stored is the cluster placed in remoteClusters and used for all routing.
	// When caching is enabled it is the overlay-wrapped cluster; otherwise the
	// raw cluster. overlay is the overlay's lifecycle Runnable (nil when off).
	stored := cl
	var overlay manager.Runnable
	if c.cacheConf.Enabled {
		wrapped, ov, werr := pendingcache.WrapCluster(cl, c.cacheConf)
		if werr != nil {
			return nil, nil, werr
		}
		stored = wrapped
		overlay = ov
	}
	c.remoteClustersMu.Lock()
	defer c.remoteClustersMu.Unlock()
	if c.remoteClusters == nil {
		c.remoteClusters = make(map[schema.GroupVersionKind][]remoteCluster)
	}
	for _, gvk := range gvks {
		log.Info("adding remote cluster for resource", "gvk", gvk, "host", host, "labels", labels, "insecureSkipTLSVerify", insecureSkipTLSVerify)
		c.remoteClusters[gvk] = append(c.remoteClusters[gvk], remoteCluster{
			cluster: stored,
			labels:  labels,
		})
	}
	// Return the raw inner cluster so the caller starts its informers/caches;
	// the overlay Runnable (if any) is returned separately.
	return cl, overlay, nil
}

// Get the gvk registered for the given resource in the home cluster's scheme.
func (c *Client) GVKFromHomeScheme(obj runtime.Object) (gvk schema.GroupVersionKind, err error) {
	gvks, unversioned, err := c.HomeScheme.ObjectKinds(obj)
	if err != nil {
		return gvk, err
	}
	if unversioned {
		return gvk, errors.New("cannot list unversioned resource")
	}
	if len(gvks) != 1 {
		return gvk, errors.New("expected exactly one gvk for list object")
	}
	return gvks[0], nil
}

// ClustersForGVK returns all clusters that serve the given GVK.
// The GVK must be explicitly configured in either homeGVKs or remoteClusters.
// Returns an error if the GVK is unknown.
func (c *Client) ClustersForGVK(gvk schema.GroupVersionKind) ([]cluster.Cluster, error) {
	c.remoteClustersMu.RLock()
	defer c.remoteClustersMu.RUnlock()
	remotes := c.remoteClusters[gvk]
	isHome := c.homeGVKs[gvk]
	if len(remotes) == 0 && !isHome {
		return nil, fmt.Errorf("gvk %s is not configured in home or any remote cluster", gvk)
	}
	clusters := make([]cluster.Cluster, 0, len(remotes)+1)
	for _, r := range remotes {
		clusters = append(clusters, r.cluster)
	}
	if isHome && c.HomeCluster != nil {
		clusters = append(clusters, c.HomeCluster)
	}
	return clusters, nil
}

// clusterForWrite uses a ResourceRouter to determine which remote cluster
// a resource should be written to based on the resource content and cluster labels.
//
// The GVK must be explicitly configured. If configured for home, the home cluster
// is returned. If configured for remotes, the ResourceRouter determines the target.
// Returns an error if the GVK is unknown or no remote cluster matches.
func (c *Client) clusterForWrite(gvk schema.GroupVersionKind, obj any) (cluster.Cluster, error) {
	c.remoteClustersMu.RLock()
	defer c.remoteClustersMu.RUnlock()

	remotes := c.remoteClusters[gvk]

	if len(remotes) > 0 {
		router, ok := c.ResourceRouters[gvk]
		if !ok {
			return nil, fmt.Errorf("no ResourceRouter configured for GVK %s with %d remote clusters", gvk, len(remotes))
		}
		for _, r := range remotes {
			match, err := router.Match(obj, r.labels)
			if err != nil {
				return nil, fmt.Errorf("resource router match error for GVK %s: %w", gvk, err)
			}
			if match {
				return r.cluster, nil
			}
		}
		// No remote match — fall back to home if the GVK is also configured there.
		if c.homeGVKs[gvk] {
			return c.HomeCluster, nil
		}
		// No match and no home fallback — return an error.
		selector, err := router.extractClusterSelector(obj)
		if err != nil {
			return nil, fmt.Errorf("failed to extract cluster selector for GVK %s: %w", gvk, err)
		}
		return nil, &NoClusterMatchedError{GVK: gvk, ClusterSelector: selector}
	}

	// No remotes configured for this GVK — fall back to home if available.
	if c.homeGVKs[gvk] {
		return c.HomeCluster, nil
	}
	return nil, &NoClusterMatchedError{GVK: gvk}
}

type duplicateError struct{ msg string }

func (e *duplicateError) Error() string { return e.msg }

// IsDuplicateError returns true if the error indicates that a resource was
// found in multiple clusters. This can be used by callers of the Get and List
// methods to keep using the result even if a duplicate exists, as long as they
// don't mind that the result is potentially inconsistent.
func IsDuplicateError(err error) bool {
	var de *duplicateError
	return errors.As(err, &de)
}

// NoClusterMatchedError is returned when a write operation cannot find a
// matching remote cluster for the given GVK and resource. This typically
// happens in multi-AZ setups where the resource targets an AZ that has no
// configured cluster.
type NoClusterMatchedError struct {
	GVK             schema.GroupVersionKind
	ClusterSelector string
}

func (e *NoClusterMatchedError) Error() string {
	return fmt.Sprintf("no cluster matched for GVK %s: cluster selector %q did not match any candidate", e.GVK, e.ClusterSelector)
}

// IsNoClusterMatchedError returns true if the error indicates that no
// configured cluster matched the resource for a write operation. Callers
// can use this to skip resources targeting unavailable AZs gracefully.
func IsNoClusterMatchedError(err error) bool {
	var nce *NoClusterMatchedError
	return errors.As(err, &nce)
}

// ConfiguredRouteLabels returns the routing label sets of all configured
// remote clusters for the given GVK. This can be used to determine which
// availability zones (or other routing dimensions) are served.
// Returns nil if the GVK is only configured for the home cluster.
func (c *Client) ConfiguredRouteLabels(gvk schema.GroupVersionKind) []map[string]string {
	c.remoteClustersMu.RLock()
	defer c.remoteClustersMu.RUnlock()
	remotes := c.remoteClusters[gvk]
	if len(remotes) == 0 {
		return nil
	}
	result := make([]map[string]string, 0, len(remotes))
	for _, r := range remotes {
		if r.labels == nil {
			result = append(result, nil)
			continue
		}
		cp := make(map[string]string, len(r.labels))
		maps.Copy(cp, r.labels)
		result = append(result, cp)
	}
	return result
}

// Get iterates over all clusters with the GVK and returns the result.
//
// If the requested resource is encountered in multiple clusters, this function
// will return the first one, but will set an error message that can be checked
// with IsDuplicateError. In that way the result can be used if the caller
// just cares about the resource existing in at least one cluster, and doesn't
// mind which one is returned.
//
// If no cluster has the resource, a NotFound error is returned.
//
// Non-NotFound errors from individual clusters are logged and silently skipped
// so that a single unavailable cluster does not block the entire read path.
func (c *Client) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	log := ctrl.LoggerFrom(ctx)
	gvk, err := c.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	clusters, err := c.ClustersForGVK(gvk)
	if err != nil {
		return err
	}
	found := false
	for _, cl := range clusters {
		// If we already found the resource in a previous cluster, we want to check if it also exists in this cluster to detect duplicates.
		if found {
			candidate := obj.DeepCopyObject().(client.Object)
			err := cl.GetClient().Get(ctx, key, candidate, opts...)
			if err == nil {
				// In this case Get() was already called and the object set.
				if c.Monitor != nil {
					c.Monitor.recordCrossClusterNameConflict("get", gvk)
				}
				return &duplicateError{msg: fmt.Sprintf("duplicate %s %s/%s in multiple clusters",
					gvk, key.Namespace, key.Name)}
			}
			if !apierrors.IsNotFound(err) {
				log.Error(err, "error checking for duplicate resource in cluster",
					"gvk", gvk, "namespace", key.Namespace, "name", key.Name,
					"host", cl.GetConfig().Host)
			}
			continue
		}

		err := cl.GetClient().Get(ctx, key, obj, opts...)
		if err == nil {
			found = true
			continue
		}
		if !apierrors.IsNotFound(err) {
			log.Error(err, "error getting resource from cluster", "gvk", gvk,
				"namespace", key.Namespace, "name", key.Name,
				"host", cl.GetConfig().Host)
		}
	}
	if !found {
		return apierrors.NewNotFound(schema.GroupResource{Group: gvk.Group, Resource: gvk.Kind}, key.Name)
	}
	return nil
}

// List iterates over all clusters with the GVK and returns a combined list
// containing all resources found in any cluster.
//
// If resources are encountered in multiple clusters with the same
// namespace/name, this function will still return a combined list of all
// resources, but will set an error message that can be checked with
// IsDuplicateError. In that way the result can be used if duplicates are ok
// and disambiguated by the caller.
//
// Errors from individual clusters are logged and silently skipped so that a
// single unavailable cluster does not block the entire read path.
func (c *Client) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	log := ctrl.LoggerFrom(ctx)
	gvk, err := c.GVKFromHomeScheme(list)
	if err != nil {
		return err
	}
	clusters, err := c.ClustersForGVK(gvk)
	if err != nil {
		return err
	}

	var allItems []runtime.Object
	for _, cl := range clusters {
		listCopy := list.DeepCopyObject().(client.ObjectList)
		if err := cl.GetClient().List(ctx, listCopy, opts...); err != nil {
			log.Error(err, "error listing resources from cluster",
				"gvk", gvk, "host", cl.GetConfig().Host)
			continue
		}
		items, err := meta.ExtractList(listCopy)
		if err != nil {
			return err
		}
		allItems = append(allItems, items...)
	}

	// Check for duplicate namespace/name pairs across clusters.
	seen := make(map[string]bool, len(allItems))
	var duplicates []string
	for _, item := range allItems {
		accessor, err := meta.Accessor(item)
		if err != nil {
			return fmt.Errorf("failed to access object metadata: %w", err)
		}
		key := accessor.GetNamespace() + "/" + accessor.GetName()
		if _, exists := seen[key]; exists {
			duplicates = append(duplicates, key)
			continue
		}
		seen[key] = true
	}
	if err := meta.SetList(list, allItems); err != nil {
		return err
	}
	if len(duplicates) > 0 {
		if c.Monitor != nil {
			c.Monitor.recordCrossClusterNameConflict("list", gvk)
		}
		return &duplicateError{msg: fmt.Sprintf("duplicate %s [%s] in multiple clusters",
			gvk, strings.Join(duplicates, ", "))}
	}
	return nil
}

// Apply is not supported in the multicluster client as the group version kind
// cannot be inferred from the ApplyConfiguration.
func (c *Client) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
	return errors.New("apply operation is not supported in multicluster client")
}

// ClusterObjectMetadata is one entry in the result of ListMetadataPerCluster.
// Labels holds the routing labels for the cluster. Items holds the object
// metadata returned by the cluster (no spec or status). IsHome is true for the
// home cluster, which has no routing labels.
type ClusterObjectMetadata struct {
	Labels map[string]string
	Items  []metav1.PartialObjectMetadata
	IsHome bool
}

// ListMetadataPerCluster returns the object metadata of the given GVK for each
// configured cluster. It uses PartialObjectMetadataList so only object metadata
// crosses the wire — no spec or status — making it efficient even for large
// object counts. Callers that only need counts can use len(Items). Clusters
// that return an error are logged and skipped (same policy as List). The home
// cluster is included with IsHome set to true.
func (c *Client) ListMetadataPerCluster(ctx context.Context, gvk schema.GroupVersionKind, opts ...client.ListOption) ([]ClusterObjectMetadata, error) {
	log := ctrl.LoggerFrom(ctx)

	c.remoteClustersMu.RLock()
	remotes := c.remoteClusters[gvk]
	isHome := c.homeGVKs[gvk]
	if len(remotes) == 0 && !isHome {
		c.remoteClustersMu.RUnlock()
		return nil, fmt.Errorf("gvk %s is not configured in home or any remote cluster", gvk)
	}
	type clusterEntry struct {
		cl     cluster.Cluster
		labels map[string]string
		isHome bool
	}
	entries := make([]clusterEntry, 0, len(remotes)+1)
	for _, r := range remotes {
		entries = append(entries, clusterEntry{cl: r.cluster, labels: maps.Clone(r.labels)})
	}
	if isHome && c.HomeCluster != nil {
		entries = append(entries, clusterEntry{cl: c.HomeCluster, isHome: true})
	}
	c.remoteClustersMu.RUnlock()

	results := make([]ClusterObjectMetadata, 0, len(entries))
	for _, e := range entries {
		partialList := &metav1.PartialObjectMetadataList{}
		partialList.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   gvk.Group,
			Version: gvk.Version,
			Kind:    gvk.Kind,
		})
		if err := e.cl.GetClient().List(ctx, partialList, opts...); err != nil {
			log.Error(err, "error listing resource metadata from cluster",
				"gvk", gvk, "host", e.cl.GetConfig().Host)
			continue
		}
		results = append(results, ClusterObjectMetadata{Labels: e.labels, Items: partialList.Items, IsHome: e.isHome})
	}
	return results, nil
}

// Create routes the object to the matching cluster using the ResourceRouter
// and performs a Create operation.
//
// Before writing, it performs a best-effort Get against the other clusters
// serving the same GVK to detect a cross-cluster name collision. If the object
// name already exists on another cluster, a duplicateError is returned (checkable
// with IsDuplicateError) and no create is performed. Non-NotFound errors from the
// probe clusters are logged and ignored so that a single unavailable cluster does
// not block writes.
func (c *Client) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	log := ctrl.LoggerFrom(ctx)
	gvk, err := c.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	cl, err := c.clusterForWrite(gvk, obj)
	if err != nil {
		return err
	}

	// Best-effort cross-cluster name collision check: the same namespace/name
	// must not already exist on another cluster serving this GVK, otherwise
	// reads would fan out to a duplicate (see IsDuplicateError).
	clusters, err := c.ClustersForGVK(gvk)
	if err != nil {
		return err
	}
	key := client.ObjectKeyFromObject(obj)
	for _, other := range clusters {
		if other == cl {
			continue
		}
		candidate := obj.DeepCopyObject().(client.Object)
		getErr := other.GetClient().Get(ctx, key, candidate)
		if getErr == nil {
			if c.Monitor != nil {
				c.Monitor.recordCrossClusterNameConflict("create", gvk)
			}
			return &duplicateError{msg: fmt.Sprintf("cannot create %s %s/%s: already exists on another cluster",
				gvk, key.Namespace, key.Name)}
		}
		if !apierrors.IsNotFound(getErr) {
			log.Error(getErr, "error checking for cross-cluster name conflict before create",
				"gvk", gvk, "namespace", key.Namespace, "name", key.Name,
				"host", other.GetConfig().Host)
		}
	}

	return cl.GetClient().Create(ctx, obj, opts...)
}

// Delete routes the object to the matching cluster using the ResourceRouter
// and performs a Delete operation.
func (c *Client) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	gvk, err := c.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	cl, err := c.clusterForWrite(gvk, obj)
	if err != nil {
		return err
	}
	return cl.GetClient().Delete(ctx, obj, opts...)
}

// Update routes the object to the matching cluster using the ResourceRouter
// and performs an Update operation.
func (c *Client) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	gvk, err := c.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	cl, err := c.clusterForWrite(gvk, obj)
	if err != nil {
		return err
	}
	return cl.GetClient().Update(ctx, obj, opts...)
}

// Patch routes the object to the matching cluster using the ResourceRouter
// and performs a Patch operation.
func (c *Client) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	gvk, err := c.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	cl, err := c.clusterForWrite(gvk, obj)
	if err != nil {
		return err
	}
	return cl.GetClient().Patch(ctx, obj, patch, opts...)
}

// DeleteAllOf iterates over all clusters with the GVK and performs DeleteAllOf on each.
func (c *Client) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	gvk, err := c.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	clusters, err := c.ClustersForGVK(gvk)
	if err != nil {
		return err
	}
	for _, cl := range clusters {
		if err := cl.GetClient().DeleteAllOf(ctx, obj, opts...); err != nil {
			return err
		}
	}
	return nil
}

// Return the scheme of the home cluster.
func (c *Client) Scheme() *runtime.Scheme {
	return c.HomeCluster.GetClient().Scheme()
}

// Return the RESTMapper of the home cluster.
func (c *Client) RESTMapper() meta.RESTMapper {
	return c.HomeCluster.GetClient().RESTMapper()
}

// Return the GroupVersionKind for the given object using the home cluster's RESTMapper.
func (c *Client) GroupVersionKindFor(obj runtime.Object) (schema.GroupVersionKind, error) {
	return c.HomeCluster.GetClient().GroupVersionKindFor(obj)
}

// Return true if the GroupVersionKind of the object is namespaced using the home cluster's RESTMapper.
func (c *Client) IsObjectNamespaced(obj runtime.Object) (bool, error) {
	return c.HomeCluster.GetClient().IsObjectNamespaced(obj)
}

// Provide a wrapper around the status subresource client which picks the right cluster
// based on the resource type.
func (c *Client) Status() client.StatusWriter { return &statusClient{multiclusterClient: c} }

// Wrapper around the status subresource client which routes to the correct cluster.
type statusClient struct{ multiclusterClient *Client }

// Create routes the status create to the matching cluster using the ResourceRouter.
func (c *statusClient) Create(ctx context.Context, obj, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	gvk, err := c.multiclusterClient.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	cl, err := c.multiclusterClient.clusterForWrite(gvk, obj)
	if err != nil {
		return err
	}
	return cl.GetClient().Status().Create(ctx, obj, subResource, opts...)
}

// Update routes the status update to the matching cluster using the ResourceRouter.
func (c *statusClient) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	gvk, err := c.multiclusterClient.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	cl, err := c.multiclusterClient.clusterForWrite(gvk, obj)
	if err != nil {
		return err
	}
	return cl.GetClient().Status().Update(ctx, obj, opts...)
}

// Patch routes the status patch to the matching cluster using the ResourceRouter.
func (c *statusClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	gvk, err := c.multiclusterClient.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	cl, err := c.multiclusterClient.clusterForWrite(gvk, obj)
	if err != nil {
		return err
	}
	return cl.GetClient().Status().Patch(ctx, obj, patch, opts...)
}

// Apply is not supported in the multicluster status client as the group version kind
// cannot be inferred from the ApplyConfiguration.
func (c *statusClient) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
	return errors.New("apply operation is not supported in multicluster status client")
}

// Provide a wrapper around the given subresource client which picks the right cluster
// based on the resource type.
func (c *Client) SubResource(subResource string) client.SubResourceClient {
	return &subResourceClient{
		multiclusterClient: c,
		subResource:        subResource,
	}
}

// Wrapper around a subresource client which routes to the correct cluster.
type subResourceClient struct {
	multiclusterClient *Client
	subResource        string
}

// Get iterates over all clusters with the GVK and returns the result.
//
// If the requested resource is encountered in multiple clusters, this function
// will return the first one, but will set an error message that can be checked
// with IsDuplicateError. In that way the result can be used if the caller
// just cares about the resource existing in at least one cluster, and doesn't
// mind which one is returned.
//
// If no cluster has the resource, a NotFound error is returned.
//
// Non-NotFound errors from individual clusters are logged and silently skipped
// so that a single unavailable cluster does not block the entire read path.
func (c *subResourceClient) Get(ctx context.Context, obj, subResource client.Object, opts ...client.SubResourceGetOption) error {
	log := ctrl.LoggerFrom(ctx)
	gvk, err := c.multiclusterClient.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	clusters, err := c.multiclusterClient.ClustersForGVK(gvk)
	if err != nil {
		return err
	}
	found := false
	for _, cl := range clusters {
		// If we already found the resource in a previous cluster, we want to check if it also exists in this cluster to detect duplicates.
		if found {
			candidateObj := obj.DeepCopyObject().(client.Object)
			candidateSub := subResource.DeepCopyObject().(client.Object)
			err := cl.GetClient().SubResource(c.subResource).
				Get(ctx, candidateObj, candidateSub, opts...)
			if err == nil {
				// In this case Get() was already called and the object set.
				if c.multiclusterClient.Monitor != nil {
					c.multiclusterClient.Monitor.recordCrossClusterNameConflict("subresource_get", gvk)
				}
				return &duplicateError{msg: fmt.Sprintf("duplicate %s %s/%s subresource %s in multiple clusters",
					gvk, candidateObj.GetNamespace(), candidateObj.GetName(), c.subResource)}
			}
			if !apierrors.IsNotFound(err) {
				log.Error(err, "error checking for duplicate sub-resource in cluster",
					"gvk", gvk, "namespace", obj.GetNamespace(), "name", obj.GetName(),
					"subresource", c.subResource, "host", cl.GetConfig().Host)
			}
			continue
		}

		err := cl.GetClient().SubResource(c.subResource).
			Get(ctx, obj, subResource, opts...)
		if err == nil {
			found = true
			continue
		}
		if !apierrors.IsNotFound(err) {
			log.Error(err, "error getting sub-resource from cluster", "gvk", gvk,
				"namespace", obj.GetNamespace(), "name", obj.GetName(),
				"subresource", c.subResource, "host", cl.GetConfig().Host)
		}
	}
	if !found {
		return apierrors.NewNotFound(schema.GroupResource{Group: gvk.Group, Resource: gvk.Kind}, obj.GetName())
	}
	return nil
}

// Create routes the subresource create to the matching cluster using the ResourceRouter.
func (c *subResourceClient) Create(ctx context.Context, obj, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	gvk, err := c.multiclusterClient.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	cl, err := c.multiclusterClient.clusterForWrite(gvk, obj)
	if err != nil {
		return err
	}
	return cl.GetClient().SubResource(c.subResource).Create(ctx, obj, subResource, opts...)
}

// Update routes the subresource update to the matching cluster using the ResourceRouter.
func (c *subResourceClient) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	gvk, err := c.multiclusterClient.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	cl, err := c.multiclusterClient.clusterForWrite(gvk, obj)
	if err != nil {
		return err
	}
	return cl.GetClient().SubResource(c.subResource).Update(ctx, obj, opts...)
}

// Patch routes the subresource patch to the matching cluster using the ResourceRouter.
func (c *subResourceClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	gvk, err := c.multiclusterClient.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	cl, err := c.multiclusterClient.clusterForWrite(gvk, obj)
	if err != nil {
		return err
	}
	return cl.GetClient().SubResource(c.subResource).Patch(ctx, obj, patch, opts...)
}

// Apply is not supported in the multicluster subresource client as the group version kind
// cannot be inferred from the ApplyConfiguration.
func (c *subResourceClient) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
	return errors.New("apply operation is not supported in multicluster subresource client")
}

// Index a field for a resource in all matching cluster caches.
// Usually, you want to index the same field in both the object and list type,
// as both would be mapped to individual clients based on their GVK.
//
// Per-cluster errors are logged and skipped — a temporarily-unreachable cluster
// does not prevent index setup for the other clusters. This mirrors the List
// error-handling pattern: the affected cluster's objects will be silently absent
// from MatchingFields queries until the index is established (e.g. after restart).
func (c *Client) IndexField(ctx context.Context, obj client.Object, list client.ObjectList, field string, extractValue client.IndexerFunc) error {
	log := ctrl.LoggerFrom(ctx)
	gvk, err := c.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	gvkList, err := c.GVKFromHomeScheme(list)
	if err != nil {
		return err
	}
	// Collect all unique caches to index.
	indexed := make(map[any]bool)
	clusters, err := c.ClustersForGVK(gvk)
	if err != nil {
		return err
	}
	for _, cl := range clusters {
		ch := cl.GetCache()
		if indexed[ch] {
			continue
		}
		indexed[ch] = true
		if err := cl.GetFieldIndexer().IndexField(ctx, obj, field, extractValue); err != nil {
			log.Error(err, "failed to register field index for cluster — objects from this cluster will be absent from index queries; restart required to recover", "field", field)
			continue
		}
	}
	clustersList, err := c.ClustersForGVK(gvkList)
	if err != nil {
		return err
	}
	for _, cl := range clustersList {
		ch := cl.GetCache()
		if indexed[ch] {
			continue
		}
		indexed[ch] = true
		if err := cl.GetFieldIndexer().IndexField(ctx, obj, field, extractValue); err != nil {
			log.Error(err, "failed to register field index for cluster — objects from this cluster will be absent from index queries; restart required to recover", "field", field)
			continue
		}
	}
	return nil
}
