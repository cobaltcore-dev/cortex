// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package multicluster

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
)

// MultiClusterRecorder implements events.EventRecorder and routes events to the
// correct cluster based on the GVK of the "regarding" object. It uses the same
// routing logic as the multicluster Client's write path.
type MultiClusterRecorder struct {
	client       *Client
	homeRecorder events.EventRecorder
	recorders    map[cluster.Cluster]events.EventRecorder
}

// GetEventRecorder creates a multi-cluster-aware EventRecorder. It pre-creates
// a per-cluster recorder for the home cluster and every remote cluster currently
// registered in the client. The name parameter is passed through to each
// cluster's GetEventRecorder method (it becomes the reportingController in the
// Kubernetes Event).
func (c *Client) GetEventRecorder(name string) events.EventRecorder {
	homeRecorder := c.HomeCluster.GetEventRecorder(name)

	recorders := make(map[cluster.Cluster]events.EventRecorder)
	recorders[c.HomeCluster] = homeRecorder

	c.remoteClustersMu.RLock()
	defer c.remoteClustersMu.RUnlock()

	for _, remotes := range c.remoteClusters {
		for _, r := range remotes {
			if _, exists := recorders[r.cluster]; !exists {
				recorders[r.cluster] = r.cluster.GetEventRecorder(name)
			}
		}
	}

	return &MultiClusterRecorder{
		client:       c,
		homeRecorder: homeRecorder,
		recorders:    recorders,
	}
}

// Eventf routes the event to the cluster that owns the "regarding" object.
// Falls back to the home cluster recorder if routing fails.
func (r *MultiClusterRecorder) Eventf(regarding runtime.Object, related runtime.Object, eventtype, reason, action, note string, args ...interface{}) {
	recorder := r.recorderFor(regarding)
	recorder.Eventf(regarding, related, eventtype, reason, action, note, args...)
}

// recorderFor resolves which per-cluster recorder to use for the given object.
func (r *MultiClusterRecorder) recorderFor(obj runtime.Object) events.EventRecorder {
	if obj == nil {
		return r.homeRecorder
	}

	gvk, err := r.client.GVKFromHomeScheme(obj)
	if err != nil {
		ctrl.Log.V(1).Info("multi-cluster recorder: failed to resolve GVK, using home recorder", "error", err)
		return r.homeRecorder
	}

	cl, err := r.client.clusterForWrite(gvk, obj)
	if err != nil {
		ctrl.Log.V(1).Info("multi-cluster recorder: no cluster matched, using home recorder", "gvk", gvk, "error", err)
		return r.homeRecorder
	}

	recorder, ok := r.recorders[cl]
	if !ok {
		ctrl.Log.V(1).Info("multi-cluster recorder: no pre-created recorder for cluster, using home recorder", "gvk", gvk)
		return r.homeRecorder
	}

	return recorder
}
