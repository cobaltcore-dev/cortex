// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package multicluster

import (
	"context"
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type Resource interface{ URI() string }

type Client struct {
	HomeCluster    cluster.Cluster
	HomeRestConfig *rest.Config
	HomeScheme     *runtime.Scheme

	remoteClusters   map[string]cluster.Cluster
	remoteClustersMu sync.RWMutex
}

// Add a remote cluster which uses the same REST config as the home cluster,
// but a different host, for the given resource URI.
//
// This can be used when the remote cluster accepts the home cluster's service
// account tokens. See the kubernetes documentation on structured auth to
// learn more about jwt-based authentication across clusters.
func (c *Client) AddRemote(uri string, host string) (cluster.Cluster, error) {
	homeRestConfig := *c.HomeRestConfig
	restConfigCopy := homeRestConfig
	restConfigCopy.Host = host
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

// Get retrieves an obj for the given object key from the Kubernetes Cluster.
// obj must be a struct pointer so that obj can be updated with the response
// returned by the Server.
func (c *Client) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.HomeCluster.GetClient().Get(ctx, key, obj, opts...)
	}
	cl := c.ClusterForResource(resource.URI())
	return cl.GetClient().Get(ctx, key, obj, opts...)
}

// List retrieves list of objects for a given namespace and list options. On a
// successful call, Items field in the list will be populated with the
// result returned from the server.
func (c *Client) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	resource, ok := list.(Resource)
	if !ok {
		return c.HomeCluster.GetClient().List(ctx, list, opts...)
	}
	cl := c.ClusterForResource(resource.URI())
	return cl.GetClient().List(ctx, list, opts...)
}

// Apply applies the given apply configuration to the Kubernetes cluster.
func (c *Client) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.HomeCluster.GetClient().Apply(ctx, obj, opts...)
	}
	cl := c.ClusterForResource(resource.URI())
	return cl.GetClient().Apply(ctx, obj, opts...)
}

// Create saves the object obj in the Kubernetes cluster. obj must be a
// struct pointer so that obj can be updated with the content returned by the Server.
func (c *Client) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.HomeCluster.GetClient().Create(ctx, obj, opts...)
	}
	cl := c.ClusterForResource(resource.URI())
	return cl.GetClient().Create(ctx, obj, opts...)
}

// Delete deletes the given obj from Kubernetes cluster.
func (c *Client) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.HomeCluster.GetClient().Delete(ctx, obj, opts...)
	}
	cl := c.ClusterForResource(resource.URI())
	return cl.GetClient().Delete(ctx, obj, opts...)
}

// Update updates the given obj in the Kubernetes cluster. obj must be a
// struct pointer so that obj can be updated with the content returned by the Server.
func (c *Client) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.HomeCluster.GetClient().Update(ctx, obj, opts...)
	}
	cl := c.ClusterForResource(resource.URI())
	return cl.GetClient().Update(ctx, obj, opts...)
}

// Patch patches the given obj in the Kubernetes cluster. obj must be a
// struct pointer so that obj can be updated with the content returned by the Server.
func (c *Client) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.HomeCluster.GetClient().Patch(ctx, obj, patch, opts...)
	}
	cl := c.ClusterForResource(resource.URI())
	return cl.GetClient().Patch(ctx, obj, patch, opts...)
}

// DeleteAllOf deletes all objects of the given type matching the given options.
func (c *Client) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.HomeCluster.GetClient().DeleteAllOf(ctx, obj, opts...)
	}
	cl := c.ClusterForResource(resource.URI())
	return cl.GetClient().DeleteAllOf(ctx, obj, opts...)
}

// Scheme returns the scheme this client is using.
func (c *Client) Scheme() *runtime.Scheme {
	return c.HomeCluster.GetClient().Scheme()
}

// RESTMapper returns the rest this client is using.
func (c *Client) RESTMapper() meta.RESTMapper {
	return c.HomeCluster.GetClient().RESTMapper()
}

// GroupVersionKindFor returns the GroupVersionKind for the given object.
func (c *Client) GroupVersionKindFor(obj runtime.Object) (schema.GroupVersionKind, error) {
	return c.HomeCluster.GetClient().GroupVersionKindFor(obj)
}

// IsObjectNamespaced returns true if the GroupVersionKind of the object is namespaced.
func (c *Client) IsObjectNamespaced(obj runtime.Object) (bool, error) {
	return c.HomeCluster.GetClient().IsObjectNamespaced(obj)
}

// StatusClient knows how to create a client which can update status subresource
// for kubernetes objects.
func (c *Client) Status() client.StatusWriter { return &statusClient{multiclusterClient: c} }

type statusClient struct{ multiclusterClient *Client }

// Create saves the subResource object in the Kubernetes cluster. obj must be a
// struct pointer so that obj can be updated with the content returned by the Server.
func (c *statusClient) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.multiclusterClient.HomeCluster.GetClient().Status().Create(ctx, obj, subResource, opts...)
	}
	cl := c.multiclusterClient.ClusterForResource(resource.URI())
	return cl.GetClient().Status().Create(ctx, obj, subResource, opts...)
}

// Update updates the fields corresponding to the status subresource for the
// given obj. obj must be a struct pointer so that obj can be updated
// with the content returned by the Server.
func (c *statusClient) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.multiclusterClient.HomeCluster.GetClient().Status().Update(ctx, obj, opts...)
	}
	cl := c.multiclusterClient.ClusterForResource(resource.URI())
	return cl.GetClient().Status().Update(ctx, obj, opts...)
}

// Patch patches the given object's subresource. obj must be a struct
// pointer so that obj can be updated with the content returned by the
// Server.
func (c *statusClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.multiclusterClient.HomeCluster.GetClient().Status().Patch(ctx, obj, patch, opts...)
	}
	cl := c.multiclusterClient.ClusterForResource(resource.URI())
	return cl.GetClient().Status().Patch(ctx, obj, patch, opts...)
}

// SubResourceClientConstructor returns a subresource client for the named subResource. Known
// upstream subResources usages are:
//
//   - ServiceAccount token creation:
//     sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: "foo", Name: "bar"}}
//     token := &authenticationv1.TokenRequest{}
//     c.SubResource("token").Create(ctx, sa, token)
//
//   - Pod eviction creation:
//     pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "foo", Name: "bar"}}
//     c.SubResource("eviction").Create(ctx, pod, &policyv1.Eviction{})
//
//   - Pod binding creation:
//     pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "foo", Name: "bar"}}
//     binding := &corev1.Binding{Target: corev1.ObjectReference{Name: "my-node"}}
//     c.SubResource("binding").Create(ctx, pod, binding)
//
//   - CertificateSigningRequest approval:
//     csr := &certificatesv1.CertificateSigningRequest{
//     ObjectMeta: metav1.ObjectMeta{Namespace: "foo", Name: "bar"},
//     Status: certificatesv1.CertificateSigningRequestStatus{
//     Conditions: []certificatesv1.[]CertificateSigningRequestCondition{{
//     Type: certificatesv1.CertificateApproved,
//     Status: corev1.ConditionTrue,
//     }},
//     },
//     }
//     c.SubResource("approval").Update(ctx, csr)
//
//   - Scale retrieval:
//     dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "foo", Name: "bar"}}
//     scale := &autoscalingv1.Scale{}
//     c.SubResource("scale").Get(ctx, dep, scale)
//
//   - Scale update:
//     dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "foo", Name: "bar"}}
//     scale := &autoscalingv1.Scale{Spec: autoscalingv1.ScaleSpec{Replicas: 2}}
//     c.SubResource("scale").Update(ctx, dep, client.WithSubResourceBody(scale))
func (c *Client) SubResource(subResource string) client.SubResourceClient {
	return &subResourceClient{
		multiclusterClient: c,
		subResource:        subResource,
	}
}

type subResourceClient struct {
	multiclusterClient *Client
	subResource        string
}

func (c *subResourceClient) Get(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceGetOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.multiclusterClient.HomeCluster.GetClient().SubResource(c.subResource).Get(ctx, obj, subResource, opts...)
	}
	cl := c.multiclusterClient.ClusterForResource(resource.URI())
	return cl.GetClient().SubResource(c.subResource).Get(ctx, obj, subResource, opts...)
}

// Create saves the subResource object in the Kubernetes cluster. obj must be a
// struct pointer so that obj can be updated with the content returned by the Server.
func (c *subResourceClient) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.multiclusterClient.HomeCluster.GetClient().SubResource(c.subResource).Create(ctx, obj, subResource, opts...)
	}
	cl := c.multiclusterClient.ClusterForResource(resource.URI())
	return cl.GetClient().SubResource(c.subResource).Create(ctx, obj, subResource, opts...)
}

// Update updates the fields corresponding to the status subresource for the
// given obj. obj must be a struct pointer so that obj can be updated
// with the content returned by the Server.
func (c *subResourceClient) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.multiclusterClient.HomeCluster.GetClient().SubResource(c.subResource).Update(ctx, obj, opts...)
	}
	cl := c.multiclusterClient.ClusterForResource(resource.URI())
	return cl.GetClient().SubResource(c.subResource).Update(ctx, obj, opts...)
}

// Patch patches the given object's subresource. obj must be a struct
// pointer so that obj can be updated with the content returned by the
// Server.
func (c *subResourceClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	resource, ok := obj.(Resource)
	if !ok {
		return c.multiclusterClient.HomeCluster.GetClient().SubResource(c.subResource).Patch(ctx, obj, patch, opts...)
	}
	cl := c.multiclusterClient.ClusterForResource(resource.URI())
	return cl.GetClient().SubResource(c.subResource).Patch(ctx, obj, patch, opts...)
}

func (c *Client) NewControllerManagedBy(mgr manager.Manager) MulticlusterBuilder {
	return MulticlusterBuilder{
		Builder:            ctrl.NewControllerManagedBy(mgr),
		multiclusterClient: c,
	}
}

type MulticlusterBuilder struct {
	*builder.Builder
	multiclusterClient *Client
}

func (b MulticlusterBuilder) Named(name string) MulticlusterBuilder {
	b.Builder = b.Builder.Named(name)
	return b
}

func (b MulticlusterBuilder) Watches(object client.Object, eventHandler handler.TypedEventHandler[client.Object, reconcile.Request], predicates ...predicate.Predicate) MulticlusterBuilder {
	resource, ok := object.(Resource)
	if !ok {
		b.Builder = b.Builder.
			Watches(object, eventHandler, builder.WithPredicates(predicates...))
		return b
	}
	cl := b.multiclusterClient.ClusterForResource(resource.URI())
	clusterCache := cl.GetCache()
	b.Builder = b.Builder.
		WatchesRawSource(source.Kind(clusterCache, object, eventHandler, predicates...))
	return b
}
