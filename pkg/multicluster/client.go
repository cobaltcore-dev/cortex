// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package multicluster

import (
	"context"
	"errors"
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
)

type Client struct {
	// The cluster in which cortex is deployed.
	HomeCluster cluster.Cluster
	// The REST config for the home cluster in which cortex is deployed.
	HomeRestConfig *rest.Config
	// The scheme for the home cluster in which cortex is deployed.
	// This scheme should include all types used in the remote clusters.
	HomeScheme *runtime.Scheme

	// Remote clusters to use by resource type.
	// The clusters provided are expected to accept the home cluster's
	// service account tokens and know about the resources being managed.
	remoteClusters map[schema.GroupVersionKind]cluster.Cluster
	// Mutex to protect access to remoteClusters.
	remoteClustersMu sync.RWMutex
}

// Add a remote cluster which uses the same REST config as the home cluster,
// but a different host, for the given resource gvks.
//
// This can be used when the remote cluster accepts the home cluster's service
// account tokens. See the kubernetes documentation on structured auth to
// learn more about jwt-based authentication across clusters.
func (c *Client) AddRemote(ctx context.Context, host, caCert string, gvks ...schema.GroupVersionKind) (cluster.Cluster, error) {
	log := ctrl.LoggerFrom(ctx)
	homeRestConfig := *c.HomeRestConfig
	restConfigCopy := homeRestConfig
	restConfigCopy.Host = host
	// This must be the CA data for the remote apiserver.
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
		c.remoteClusters = make(map[schema.GroupVersionKind]cluster.Cluster)
	}
	for _, gvk := range gvks {
		log.Info("adding remote cluster for resource", "gvk", gvk, "host", host)
		c.remoteClusters[gvk] = cl
	}
	return cl, nil
}

// Get the gvk registered for the given resource in the home cluster's RESTMapper.
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

// Get the cluster for the given group version kind.
//
// If this object kind does not have a remote cluster configured,
// the home cluster is returned.
func (c *Client) ClusterForResource(gvk schema.GroupVersionKind) cluster.Cluster {
	c.remoteClustersMu.RLock()
	defer c.remoteClustersMu.RUnlock()
	cl, ok := c.remoteClusters[gvk]
	if ok {
		return cl
	}
	return c.HomeCluster
}

// Get the client for the given resource URI.
//
// If this URI does not have a remote cluster configured, the home cluster's
// Get the client for the given resource group version kind.
//
// If this object kind does not have a remote cluster configured, the home cluster's
// client is returned.
func (c *Client) ClientForResource(gvk schema.GroupVersionKind) client.Client {
	return c.
		ClusterForResource(gvk).
		GetClient()
}

// Pick the right cluster based on the resource type and perform a Get operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *Client) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	gvk, err := c.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	return c.ClientForResource(gvk).Get(ctx, key, obj, opts...)
}

// Pick the right cluster based on the resource type and perform a List operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *Client) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	gvk, err := c.GVKFromHomeScheme(list)
	if err != nil {
		return err
	}
	return c.ClientForResource(gvk).List(ctx, list, opts...)
}

// Apply is not supported in the multicluster client as the group version kind
// cannot be inferred from the ApplyConfiguration. Use ClientForResource
// and call Apply on the returned client instead.
func (c *Client) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
	return errors.New("apply operation is not supported in multicluster client")
}

// Pick the right cluster based on the resource type and perform a Create operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *Client) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	gvk, err := c.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	return c.ClientForResource(gvk).Create(ctx, obj, opts...)
}

// Pick the right cluster based on the resource type and perform a Delete operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *Client) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	gvk, err := c.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	return c.ClientForResource(gvk).Delete(ctx, obj, opts...)
}

// Pick the right cluster based on the resource type and perform an Update operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *Client) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	gvk, err := c.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	return c.ClientForResource(gvk).Update(ctx, obj, opts...)
}

// Pick the right cluster based on the resource type and perform a Patch operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *Client) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	gvk, err := c.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	return c.ClientForResource(gvk).Patch(ctx, obj, patch, opts...)
}

// Pick the right cluster based on the resource type and perform a DeleteAllOf operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *Client) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	gvk, err := c.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	return c.ClientForResource(gvk).DeleteAllOf(ctx, obj, opts...)
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

// Wrapper around the status subresource client which picks the right cluster
// based on the resource type.
type statusClient struct{ multiclusterClient *Client }

// Pick the right cluster based on the resource type and perform a Create operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *statusClient) Create(ctx context.Context, obj, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	gvk, err := c.multiclusterClient.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	return c.multiclusterClient.ClientForResource(gvk).
		Status().Create(ctx, obj, subResource, opts...)
}

// Pick the right cluster based on the resource type and perform an Update operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *statusClient) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	gvk, err := c.multiclusterClient.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	return c.multiclusterClient.ClientForResource(gvk).
		Status().Update(ctx, obj, opts...)
}

// Pick the right cluster based on the resource type and perform a Patch operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *statusClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	gvk, err := c.multiclusterClient.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	return c.multiclusterClient.ClientForResource(gvk).
		Status().Patch(ctx, obj, patch, opts...)
}

// Apply is not supported in the multicluster status client as the group version kind
// cannot be inferred from the ApplyConfiguration. Use ClientForResource
// and call Apply on the returned client instead.
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

// Wrapper around a subresource client which picks the right cluster
// based on the resource type.
type subResourceClient struct {
	// The parent multicluster client.
	multiclusterClient *Client
	// The name of the subresource.
	subResource string
}

// Pick the right cluster based on the resource type and perform a Get operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *subResourceClient) Get(ctx context.Context, obj, subResource client.Object, opts ...client.SubResourceGetOption) error {
	gvk, err := c.multiclusterClient.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	return c.multiclusterClient.ClientForResource(gvk).
		SubResource(c.subResource).Get(ctx, obj, subResource, opts...)
}

// Pick the right cluster based on the resource type and perform a Create operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *subResourceClient) Create(ctx context.Context, obj, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	gvk, err := c.multiclusterClient.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	return c.multiclusterClient.ClientForResource(gvk).
		SubResource(c.subResource).Create(ctx, obj, subResource, opts...)
}

// Pick the right cluster based on the resource type and perform an Update operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *subResourceClient) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	gvk, err := c.multiclusterClient.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	return c.multiclusterClient.ClientForResource(gvk).
		SubResource(c.subResource).Update(ctx, obj, opts...)
}

// Pick the right cluster based on the resource type and perform a Patch operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *subResourceClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	gvk, err := c.multiclusterClient.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	return c.multiclusterClient.ClientForResource(gvk).
		SubResource(c.subResource).Patch(ctx, obj, patch, opts...)
}

// Apply is not supported in the multicluster subresource client as the group version kind
// cannot be inferred from the ApplyConfiguration. Use ClientForResource
// and call Apply on the returned client instead.
func (c *subResourceClient) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
	return errors.New("apply operation is not supported in multicluster subresource client")
}

// Index a field for a resource in the matching cluster's cache.
// Usually, you want to index the same field in both the object and list type,
// as both would be mapped to individual clients based on their GVK.
func (c *Client) IndexField(ctx context.Context, obj client.Object, list client.ObjectList, field string, extractValue client.IndexerFunc) error {
	gvkObj, err := c.GVKFromHomeScheme(obj)
	if err != nil {
		return err
	}
	if err := c.ClusterForResource(gvkObj).
		GetCache().
		IndexField(ctx, obj, field, extractValue); err != nil {
		return err
	}
	// Index the object in the list cluster as well.
	gvkList, err := c.GVKFromHomeScheme(list)
	if err != nil {
		return err
	}
	if err := c.ClusterForResource(gvkList).
		GetCache().
		IndexField(ctx, obj, field, extractValue); err != nil {
		return err
	}
	return nil
}
