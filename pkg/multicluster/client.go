// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package multicluster

import (
	"context"
	"errors"
	"fmt"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
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

	// Remote clusters to use by resource type. Multiple clusters can serve
	// the same GVK (e.g. one per availability zone).
	remoteClusters map[schema.GroupVersionKind][]remoteCluster
	// Mutex to protect access to remoteClusters.
	remoteClustersMu sync.RWMutex

	// GVKs for which a write operation falls back to the home cluster
	// when no remote cluster matches.
	fallbackGVKs map[schema.GroupVersionKind]bool
}

type ClientConfig struct {
	// Fallback GVKs that are written to the home cluster if no router match is found.
	Fallbacks []FallbackConfig `json:"fallbacks,omitempty"`
	// Apiserver overrides that map GVKs to remote clusters.
	APIServerOverrides []APIServerOverride `json:"apiservers,omitempty"`
}

// FallbackConfig specifies a GVK that falls back to the home cluster for writes
// when no remote cluster matches.
type FallbackConfig struct {
	// The resource GVK formatted as "<group>/<version>/<Kind>".
	GVK string `json:"gvk"`
}

// APIServerOverride maps multiple GVKs to a remote kubernetes apiserver with
// routing labels. It is assumed that the remote apiserver accepts the
// serviceaccount tokens issued by the local cluster.
type APIServerOverride struct {
	// The remote kubernetes apiserver url, e.g. "https://my-apiserver:6443".
	Host string `json:"host"`
	// The root CA certificate to verify the remote apiserver.
	CACert string `json:"caCert,omitempty"`
	// The resource GVKs this apiserver serves, formatted as "<group>/<version>/<Kind>".
	GVKs []string `json:"gvks"`
	// Labels used by ResourceRouters to match resources to this cluster
	// for write operations (Create/Update/Delete/Patch).
	Labels map[string]string `json:"labels,omitempty"`
}

// Helper function to initialize a new multicluster client during service startup,
// using the conf module provided by cortex.
func (c *Client) InitFromConf(ctx context.Context, mgr ctrl.Manager, conf ClientConfig) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("initializing multicluster client with config", "config", conf)
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
	// Parse fallback GVKs.
	c.fallbackGVKs = make(map[schema.GroupVersionKind]bool)
	for _, fb := range conf.Fallbacks {
		gvk, ok := gvksByConfStr[fb.GVK]
		if !ok {
			return errors.New("no gvk registered for fallback " + fb.GVK)
		}
		log.Info("registering fallback gvk", "gvk", gvk)
		c.fallbackGVKs[gvk] = true
	}
	// Parse apiserver overrides.
	for _, override := range conf.APIServerOverrides {
		var resolvedGVKs []schema.GroupVersionKind
		for _, gvkStr := range override.GVKs {
			gvk, ok := gvksByConfStr[gvkStr]
			if !ok {
				return errors.New("no gvk registered for API server override " + gvkStr)
			}
			resolvedGVKs = append(resolvedGVKs, gvk)
		}
		cl, err := c.AddRemote(ctx, override.Host, override.CACert, override.Labels, resolvedGVKs...)
		if err != nil {
			return err
		}
		if err := mgr.Add(cl); err != nil {
			return err
		}
	}
	return nil
}

// Add a remote cluster which uses the same REST config as the home cluster,
// but a different host, for the given resource gvks.
//
// This can be used when the remote cluster accepts the home cluster's service
// account tokens. See the kubernetes documentation on structured auth to
// learn more about jwt-based authentication across clusters.
func (c *Client) AddRemote(ctx context.Context, host, caCert string, labels map[string]string, gvks ...schema.GroupVersionKind) (cluster.Cluster, error) {
	log := ctrl.LoggerFrom(ctx)
	homeRestConfig := *c.HomeRestConfig
	restConfigCopy := homeRestConfig
	restConfigCopy.Host = host
	restConfigCopy.CAData = []byte(caCert)
	cl, err := cluster.New(&restConfigCopy, func(o *cluster.Options) {
		o.Scheme = c.HomeScheme
	})
	if err != nil {
		return nil, err
	}
	c.remoteClustersMu.Lock()
	defer c.remoteClustersMu.Unlock()
	if c.remoteClusters == nil {
		c.remoteClusters = make(map[schema.GroupVersionKind][]remoteCluster)
	}
	for _, gvk := range gvks {
		log.Info("adding remote cluster for resource", "gvk", gvk, "host", host, "labels", labels)
		c.remoteClusters[gvk] = append(c.remoteClusters[gvk], remoteCluster{
			cluster: cl,
			labels:  labels,
		})
	}
	return cl, nil
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
// If no remote clusters are configured, only the home cluster is returned.
// For fallback GVKs with remote clusters, the home cluster is appended
// because resources might have been written there as a fallback.
func (c *Client) ClustersForGVK(gvk schema.GroupVersionKind) []cluster.Cluster {
	c.remoteClustersMu.RLock()
	defer c.remoteClustersMu.RUnlock()
	remotes := c.remoteClusters[gvk]
	if len(remotes) == 0 {
		return []cluster.Cluster{c.HomeCluster}
	}
	clusters := make([]cluster.Cluster, 0, len(remotes)+1)
	for _, r := range remotes {
		clusters = append(clusters, r.cluster)
	}
	if c.fallbackGVKs[gvk] {
		clusters = append(clusters, c.HomeCluster)
	}
	return clusters
}

// clusterForWrite uses a ResourceRouter to determine which remote cluster
// a resource should be written to based on the resource content and cluster labels.
//
// If no remote clusters are configured for the GVK, the home cluster is returned.
// If a ResourceRouter is configured and matches a cluster, that cluster is returned.
// If no match is found and the GVK has a fallback configured, the home cluster is returned.
// Otherwise an error is returned.
func (c *Client) clusterForWrite(gvk schema.GroupVersionKind, obj any) (cluster.Cluster, error) {
	c.remoteClustersMu.RLock()
	defer c.remoteClustersMu.RUnlock()
	remotes := c.remoteClusters[gvk]
	if len(remotes) == 0 {
		return c.HomeCluster, nil
	}
	router, ok := c.ResourceRouters[gvk]
	if !ok {
		// If there are more than one remote cluster and no router, we don't know which one to write to.
		// That's why we need to return an error in that case. If there's only one remote cluster, we can safely assume
		if len(remotes) == 1 {
			return remotes[0].cluster, nil
		}
		return nil, fmt.Errorf("no ResourceRouter configured for GVK %s with %d clusters", gvk, len(remotes))
	}
	for _, r := range remotes {
		match, err := router.Match(obj, r.labels)
		if err != nil {
			return nil, fmt.Errorf("ResourceRouter match error for GVK %s: %w", gvk, err)
		}
		if match {
			return r.cluster, nil
		}
	}

	// If we can't match any remote cluster but the GVK is configured to fall back to the home cluster, return the home cluster.
	if c.fallbackGVKs[gvk] {
		return c.HomeCluster, nil
	}
	return nil, fmt.Errorf("no cluster matched for GVK %s and no fallback configured", gvk)
}

// Get iterates over all clusters with the GVK and returns the first result found.
// If no cluster has the resource, the last NotFound error is returned.
func (c *Client) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	gvk, err := c.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	clusters := c.ClustersForGVK(gvk)
	var lastErr error
	for _, cl := range clusters {
		err := cl.GetClient().Get(ctx, key, obj, opts...)
		if err == nil {
			return nil
		}
		if !apierrors.IsNotFound(err) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

// List iterates over all clusters with the GVK and returns a combined list.
func (c *Client) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	gvk, err := c.GVKFromHomeScheme(list)
	if err != nil {
		return err
	}
	clusters := c.ClustersForGVK(gvk)

	var allItems []runtime.Object
	for _, cl := range clusters {
		listCopy := list.DeepCopyObject().(client.ObjectList)
		if err := cl.GetClient().List(ctx, listCopy, opts...); err != nil {
			return err
		}
		items, err := meta.ExtractList(listCopy)
		if err != nil {
			return err
		}
		allItems = append(allItems, items...)
	}
	return meta.SetList(list, allItems)
}

// Apply is not supported in the multicluster client as the group version kind
// cannot be inferred from the ApplyConfiguration.
func (c *Client) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
	return errors.New("apply operation is not supported in multicluster client")
}

// Create routes the object to the matching cluster using the ResourceRouter
// and performs a Create operation.
func (c *Client) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	gvk, err := c.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	cl, err := c.clusterForWrite(gvk, obj)
	if err != nil {
		return err
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
	for _, cl := range c.ClustersForGVK(gvk) {
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

// Get iterates over all clusters with the GVK and returns the first result found.
func (c *subResourceClient) Get(ctx context.Context, obj, subResource client.Object, opts ...client.SubResourceGetOption) error {
	gvk, err := c.multiclusterClient.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	clusters := c.multiclusterClient.ClustersForGVK(gvk)
	var lastErr error
	for _, cl := range clusters {
		err := cl.GetClient().SubResource(c.subResource).Get(ctx, obj, subResource, opts...)
		if err == nil {
			return nil
		}
		if !apierrors.IsNotFound(err) {
			return err
		}
		lastErr = err
	}
	return lastErr
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
func (c *Client) IndexField(ctx context.Context, obj client.Object, list client.ObjectList, field string, extractValue client.IndexerFunc) error {
	gvkObj, err := c.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	gvkList, err := c.GVKFromHomeScheme(list)
	if err != nil {
		return err
	}
	// Collect all unique caches to index.
	type cacheKey struct{}
	indexed := make(map[any]bool)
	for _, cl := range c.ClustersForGVK(gvkObj) {
		ch := cl.GetCache()
		if indexed[ch] {
			continue
		}
		indexed[ch] = true
		if err := ch.IndexField(ctx, obj, field, extractValue); err != nil {
			return err
		}
	}
	for _, cl := range c.ClustersForGVK(gvkList) {
		ch := cl.GetCache()
		if indexed[ch] {
			continue
		}
		indexed[ch] = true
		if err := ch.IndexField(ctx, obj, field, extractValue); err != nil {
			return err
		}
	}
	return nil
}
