// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package multicluster

import (
	"context"
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
)

type Resource interface{ URI() string }

type Client struct {
	// The cluster in which cortex is deployed.
	HomeCluster cluster.Cluster
	// The REST config for the home cluster in which cortex is deployed.
	HomeRestConfig *rest.Config
	// The scheme for the home cluster in which cortex is deployed.
	// This scheme should include all types used in the remote clusters.
	HomeScheme *runtime.Scheme

	// Remote clusters to use by resource URI.
	// The clusters provided are expected to accept the home cluster's
	// service account tokens and know about the resources being managed.
	remoteClusters map[string]cluster.Cluster
	// Mutex to protect access to remoteClusters.
	remoteClustersMu sync.RWMutex
}

// Add a remote cluster which uses the same REST config as the home cluster,
// but a different host, for the given resource URI.
//
// This can be used when the remote cluster accepts the home cluster's service
// account tokens. See the kubernetes documentation on structured auth to
// learn more about jwt-based authentication across clusters.
func (c *Client) AddRemote(uri, host, caCert string) (cluster.Cluster, error) {
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
		c.remoteClusters = make(map[string]cluster.Cluster)
	}
	c.remoteClusters[uri] = cl
	return cl, nil
}

// Get the cluster for the given resource URI.
//
// If this URI does not have a remote cluster configured, the home cluster
// is returned.
func (c *Client) ClusterForResource(uri string) cluster.Cluster {
	c.remoteClustersMu.RLock()
	defer c.remoteClustersMu.RUnlock()
	cl, ok := c.remoteClusters[uri]
	if ok {
		return cl
	}
	return c.HomeCluster
}

// Get the client for the given resource URI.
//
// If this URI does not have a remote cluster configured, the home cluster's
// client is returned.
func (c *Client) ClientForResource(uri string) client.Client {
	return c.ClusterForResource(uri).GetClient()
}

// Pick the right cluster based on the resource URI and perform a Get operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *Client) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.HomeCluster.GetClient().Get(ctx, key, obj, opts...)
	}
	cl := c.ClusterForResource(resource.URI())
	return cl.GetClient().Get(ctx, key, obj, opts...)
}

// Pick the right cluster based on the resource URI and perform a List operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *Client) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	resource, ok := list.(Resource)
	if !ok {
		return c.HomeCluster.GetClient().List(ctx, list, opts...)
	}
	cl := c.ClusterForResource(resource.URI())
	return cl.GetClient().List(ctx, list, opts...)
}

// Pick the right cluster based on the resource URI and perform an Apply operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *Client) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.HomeCluster.GetClient().Apply(ctx, obj, opts...)
	}
	cl := c.ClusterForResource(resource.URI())
	return cl.GetClient().Apply(ctx, obj, opts...)
}

// Pick the right cluster based on the resource URI and perform a Create operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *Client) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.HomeCluster.GetClient().Create(ctx, obj, opts...)
	}
	cl := c.ClusterForResource(resource.URI())
	return cl.GetClient().Create(ctx, obj, opts...)
}

// Pick the right cluster based on the resource URI and perform a Create operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *Client) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.HomeCluster.GetClient().Delete(ctx, obj, opts...)
	}
	cl := c.ClusterForResource(resource.URI())
	return cl.GetClient().Delete(ctx, obj, opts...)
}

// Pick the right cluster based on the resource URI and perform an Update operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *Client) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.HomeCluster.GetClient().Update(ctx, obj, opts...)
	}
	cl := c.ClusterForResource(resource.URI())
	return cl.GetClient().Update(ctx, obj, opts...)
}

// Pick the right cluster based on the resource URI and perform a Patch operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *Client) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.HomeCluster.GetClient().Patch(ctx, obj, patch, opts...)
	}
	cl := c.ClusterForResource(resource.URI())
	return cl.GetClient().Patch(ctx, obj, patch, opts...)
}

// Pick the right cluster based on the resource URI and perform a DeleteAllOf operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *Client) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.HomeCluster.GetClient().DeleteAllOf(ctx, obj, opts...)
	}
	cl := c.ClusterForResource(resource.URI())
	return cl.GetClient().DeleteAllOf(ctx, obj, opts...)
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
// based on the resource URI.
func (c *Client) Status() client.StatusWriter { return &statusClient{multiclusterClient: c} }

// Wrapper around the status subresource client which picks the right cluster
// based on the resource URI.
type statusClient struct{ multiclusterClient *Client }

// Pick the right cluster based on the resource URI and perform a Create operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *statusClient) Create(ctx context.Context, obj, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.multiclusterClient.HomeCluster.GetClient().Status().Create(ctx, obj, subResource, opts...)
	}
	cl := c.multiclusterClient.ClusterForResource(resource.URI())
	return cl.GetClient().Status().Create(ctx, obj, subResource, opts...)
}

// Pick the right cluster based on the resource URI and perform an Update operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *statusClient) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.multiclusterClient.HomeCluster.GetClient().Status().Update(ctx, obj, opts...)
	}
	cl := c.multiclusterClient.ClusterForResource(resource.URI())
	return cl.GetClient().Status().Update(ctx, obj, opts...)
}

// Pick the right cluster based on the resource URI and perform a Patch operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *statusClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.multiclusterClient.HomeCluster.GetClient().Status().Patch(ctx, obj, patch, opts...)
	}
	cl := c.multiclusterClient.ClusterForResource(resource.URI())
	return cl.GetClient().Status().Patch(ctx, obj, patch, opts...)
}

// Pick the right cluster based on the resource URI and perform an Apply operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *statusClient) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.multiclusterClient.HomeCluster.GetClient().Status().Apply(ctx, obj, opts...)
	}
	cl := c.multiclusterClient.ClusterForResource(resource.URI())
	return cl.GetClient().Status().Apply(ctx, obj, opts...)
}

// Provide a wrapper around the given subresource client which picks the right cluster
// based on the resource URI.
func (c *Client) SubResource(subResource string) client.SubResourceClient {
	return &subResourceClient{
		multiclusterClient: c,
		subResource:        subResource,
	}
}

// Wrapper around a subresource client which picks the right cluster
// based on the resource URI.
type subResourceClient struct {
	// The parent multicluster client.
	multiclusterClient *Client
	// The name of the subresource.
	subResource string
}

// Pick the right cluster based on the resource URI and perform a Get operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *subResourceClient) Get(ctx context.Context, obj, subResource client.Object, opts ...client.SubResourceGetOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.multiclusterClient.HomeCluster.GetClient().SubResource(c.subResource).Get(ctx, obj, subResource, opts...)
	}
	cl := c.multiclusterClient.ClusterForResource(resource.URI())
	return cl.GetClient().SubResource(c.subResource).Get(ctx, obj, subResource, opts...)
}

// Pick the right cluster based on the resource URI and perform a Create operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *subResourceClient) Create(ctx context.Context, obj, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.multiclusterClient.HomeCluster.GetClient().SubResource(c.subResource).Create(ctx, obj, subResource, opts...)
	}
	cl := c.multiclusterClient.ClusterForResource(resource.URI())
	return cl.GetClient().SubResource(c.subResource).Create(ctx, obj, subResource, opts...)
}

// Pick the right cluster based on the resource URI and perform an Update operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *subResourceClient) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.multiclusterClient.HomeCluster.GetClient().SubResource(c.subResource).Update(ctx, obj, opts...)
	}
	cl := c.multiclusterClient.ClusterForResource(resource.URI())
	return cl.GetClient().SubResource(c.subResource).Update(ctx, obj, opts...)
}

// Pick the right cluster based on the resource URI and perform a Patch operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *subResourceClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.multiclusterClient.HomeCluster.GetClient().SubResource(c.subResource).Patch(ctx, obj, patch, opts...)
	}
	cl := c.multiclusterClient.ClusterForResource(resource.URI())
	return cl.GetClient().SubResource(c.subResource).Patch(ctx, obj, patch, opts...)
}

// Pick the right cluster based on the resource URI and perform an Apply operation.
// If the object does not implement Resource or no custom cluster is configured,
// the home cluster is used.
func (c *subResourceClient) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.multiclusterClient.HomeCluster.GetClient().SubResource(c.subResource).Apply(ctx, obj, opts...)
	}
	cl := c.multiclusterClient.ClusterForResource(resource.URI())
	return cl.GetClient().SubResource(c.subResource).Apply(ctx, obj, opts...)
}
