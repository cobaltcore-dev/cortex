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

	// GVKs explicitly configured for the home cluster.
	homeGVKs map[schema.GroupVersionKind]bool
}

type ClientConfig struct {
	// Apiserver configuration mapping GVKs to home or remote clusters.
	// Every GVK used through the multicluster client must be listed
	// in either Home or Remotes. Unknown GVKs will cause an error.
	APIServers APIServersConfig `json:"apiservers"`
}

// APIServersConfig separates resources into home and remote clusters.
type APIServersConfig struct {
	// Resources managed in the cluster where cortex is deployed.
	Home HomeConfig `json:"home"`
	// Resources managed in remote clusters.
	Remotes []RemoteConfig `json:"remotes,omitempty"`
}

// HomeConfig lists GVKs that are managed in the home cluster.
type HomeConfig struct {
	// The resource GVKs formatted as "<group>/<version>/<Kind>".
	GVKs []string `json:"gvks"`
}

// RemoteConfig maps multiple GVKs to a remote kubernetes apiserver with
// routing labels. It is assumed that the remote apiserver accepts the
// serviceaccount tokens issued by the local cluster.
type RemoteConfig struct {
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
		cl, err := c.AddRemote(ctx, remote.Host, remote.CACert, remote.Labels, resolvedGVKs...)
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
// The GVK must be explicitly configured in either homeGVKs or remoteClusters.
// Returns an error if the GVK is unknown.
func (c *Client) ClustersForGVK(gvk schema.GroupVersionKind) ([]cluster.Cluster, error) {
	c.remoteClustersMu.RLock()
	defer c.remoteClustersMu.RUnlock()
	remotes := c.remoteClusters[gvk]
	isHome := c.homeGVKs[gvk]
	if len(remotes) == 0 && !isHome {
		return nil, fmt.Errorf("GVK %s is not configured in home or any remote cluster", gvk)
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
	}

	// If we couldn't find a matching remote cluster (not configured or not found) but the GVK is configured for home, return the home cluster.
	if c.homeGVKs[gvk] {
		return c.HomeCluster, nil
	}
	return nil, fmt.Errorf("no cluster matched for GVK %s", gvk)
}

// Get iterates over all clusters with the GVK and returns the result.
// Returns an error if the resource is found in multiple clusters (duplicate).
// If no cluster has the resource, the last NotFound error is returned.
func (c *Client) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
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
				return fmt.Errorf("duplicate resource found: %s %s/%s exists in multiple clusters", gvk, key.Namespace, key.Name)
			}
			if !apierrors.IsNotFound(err) {
				return err
			}
			continue
		}

		err := cl.GetClient().Get(ctx, key, obj, opts...)
		if err == nil {
			found = true
			continue
		}
		if !apierrors.IsNotFound(err) {
			return err
		}
	}
	if !found {
		return apierrors.NewNotFound(schema.GroupResource{Group: gvk.Group, Resource: gvk.Kind}, key.Name)
	}
	return nil
}

// List iterates over all clusters with the GVK and returns a combined list.
// Returns an error if any resources share the same namespace/name across clusters.
func (c *Client) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
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
			return err
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
	if len(duplicates) > 0 {
		return fmt.Errorf("duplicate resources found in multiple clusters for %s: %v", gvk, duplicates)
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
// Returns an error if the resource is found in multiple clusters (duplicate).
func (c *subResourceClient) Get(ctx context.Context, obj, subResource client.Object, opts ...client.SubResourceGetOption) error {
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
		if found {
			candidateObj := obj.DeepCopyObject().(client.Object)
			candidateSub := subResource.DeepCopyObject().(client.Object)
			err := cl.GetClient().SubResource(c.subResource).Get(ctx, candidateObj, candidateSub, opts...)
			if err == nil {
				return fmt.Errorf("duplicate sub-resource found: %s %s/%s exists in multiple clusters", gvk, obj.GetNamespace(), obj.GetName())
			}
			if !apierrors.IsNotFound(err) {
				return err
			}
			continue
		}

		err := cl.GetClient().SubResource(c.subResource).Get(ctx, obj, subResource, opts...)
		if err == nil {
			found = true
			continue
		}
		if !apierrors.IsNotFound(err) {
			return err
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
func (c *Client) IndexField(ctx context.Context, obj client.Object, list client.ObjectList, field string, extractValue client.IndexerFunc) error {
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
		if err := ch.IndexField(ctx, obj, field, extractValue); err != nil {
			return err
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
		if err := ch.IndexField(ctx, obj, field, extractValue); err != nil {
			return err
		}
	}
	return nil
}
