// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package pods

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/api/delegation/pods"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/pods/helpers"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type PodGroupSetController struct {
	client.Client

	// TODO: the pods filter-weigher-pipeline controller and this controller both
	// have a seperate mutex which means they are both allowed to bind pods at the
	// same time which opens up race conditions due to the delayed updates of the
	// node's allocatable resources
	// Mutex to only allow one process at a time
	processMu sync.Mutex

	// SchedulerContext holds state of the scheduling cache and queue
	SCtx *SchedulerContext

	podPipeline lib.FilterWeigherPipeline[pods.PodPipelineRequest]

	// Monitor to pass down to the pipeline
	Monitor lib.FilterWeigherPipelineMonitor
	// Config for the scheduling operator
	Conf conf.Config
	// Recorder for publishing Event objects
	Recorder events.EventRecorder
}

func (c *PodGroupSetController) ProcessNewPodGroupSet(ctx context.Context, pgs *v1alpha1.PodGroupSet) error {
	c.processMu.Lock()
	defer c.processMu.Unlock()

	log := ctrl.LoggerFrom(ctx)
	startedAt := time.Now()

	podGroupSetResourceRequests := make(corev1.ResourceList)
	for _, group := range pgs.Spec.PodGroups {
		for range group.Spec.Replicas {
			podResources := helpers.GetPodResourceRequests(corev1.Pod{Spec: group.Spec.PodSpec})
			helpers.AddResourcesInto(podGroupSetResourceRequests, podResources)
		}
	}

	topology := c.SCtx.Cache.GetTopology()

	var bestPlacements map[string]string
	var bestWeight float64

	for _, level := range slices.Backward(topology.Levels) {
		for _, topologyNode := range topology.Nodes[level] {
			canFit := true
			for resourceName, requestedQty := range podGroupSetResourceRequests {
				allocatableQty, exists := topologyNode.Allocatable[resourceName]
				log.V(1).Info("Checking resource allocation",
					"topologyNode", topologyNode.Name,
					"resourceName", resourceName,
					"requestedQty", requestedQty.String(),
					"allocatableQty", func() string {
						if exists {
							return allocatableQty.String()
						}
						return "not available"
					}(),
					"exists", exists)

				if !exists || requestedQty.Cmp(allocatableQty) > 0 {
					log.V(1).Info("Resource requirement not met",
						"topologyNode", topologyNode.Name,
						"resourceName", resourceName,
						"requestedQty", requestedQty.String(),
						"allocatableQty", func() string {
							if exists {
								return allocatableQty.String()
							}
							return "not available"
						}(),
						"reason", func() string {
							if !exists {
								return "resource not available"
							}
							return "insufficient capacity"
						}())
					canFit = false
					break
				}
			}
			if !canFit {
				continue
			}

			placements, weight, err := c.getPodGroupSetPlacement(pgs, topologyNode.Nodes, c.podPipeline)
			if err != nil {
				log.V(1).Error(err, "failed to schedule PodGroupSet")
				continue
			}

			if len(placements) == 0 {
				continue
			}

			if weight > bestWeight {
				bestPlacements = placements
				bestWeight = weight
			}
		}
		if len(bestPlacements) > 0 {
			break
		}
	}

	if len(bestPlacements) > 0 {
		if err := c.createPods(ctx, pgs, bestPlacements); err != nil {
			return err
		}
		c.Recorder.Eventf(pgs, nil, corev1.EventTypeNormal, "Scheduled", "SchedulePGS", "Successfully created pods of %s/%s", pgs.Namespace, pgs.Name)
	} else {
		log.Info("no suitable placement found", "PodGroupSet", pgs.Name)
		c.Recorder.Eventf(pgs, nil, corev1.EventTypeWarning, "FailedScheduling", "SchedulePGS", "No suitable placement found for PodGroupSet %s (%s)", pgs.Name, pgs.Namespace)

		// Add PodGroupSet to scheduling queue for retry when resources become available
		if c.SCtx.Queue != nil {
			log.Info("Adding PodGroupSet to scheduling queue due to scheduling failure", "PodGroupSet", pgs.Name, "namespace", pgs.Namespace)
			c.SCtx.Queue.AddPodGroupSet(pgs)
		}
	}

	log.Info("PodGroupSet processed", "duration", time.Since(startedAt))
	return nil
}

func (c *PodGroupSetController) getPodGroupSetPlacement(pgs *v1alpha1.PodGroupSet, nodes []corev1.Node, podPipeline lib.FilterWeigherPipeline[pods.PodPipelineRequest]) (map[string]string, float64, error) {
	nodePool := make([]corev1.Node, len(nodes))
	for i, node := range nodes {
		nodePool[i] = *node.DeepCopy()
	}

	targetPlacements := make(map[string]string)
	placementWeight := 0.0

	for _, group := range pgs.Spec.PodGroups {
		for i := range int(group.Spec.Replicas) {
			podName := pgs.PodName(group.Name, i)

			podRequest := pods.PodPipelineRequest{
				Nodes: nodePool,
				Pod: corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      podName,
						Namespace: pgs.Namespace,
					},
					Spec: group.Spec.PodSpec,
				},
			}

			result, err := podPipeline.Run(podRequest)
			if err != nil {
				return nil, 0, fmt.Errorf("pod pipeline failed for pod %s: %w", podName, err)
			}
			if result.TargetHost == nil {
				return nil, 0, nil
			}

			nodeName := *result.TargetHost
			targetPlacements[podName] = nodeName
			placementWeight += result.AggregatedOutWeights[nodeName]

			podResourceRequests := helpers.GetPodResourceRequests(podRequest.Pod)
			for i := range nodePool {
				if nodePool[i].Name == nodeName {
					helpers.SubtractResourcesInto(nodePool[i].Status.Allocatable, podResourceRequests)
					break
				}
			}
		}
	}

	return targetPlacements, placementWeight, nil
}

func (c *PodGroupSetController) createPods(ctx context.Context, pgs *v1alpha1.PodGroupSet, placements map[string]string) error {
	log := ctrl.LoggerFrom(ctx)

	// TODO: this needs to happen in two steps:
	// 1. Create a PodReservation (new CR) for each pod
	// If not successfull, delete reservations and reprocess PGS
	// 2. Create pods and bind to node with respective reservation
	// and in doing so delete the reservations

	for _, group := range pgs.Spec.PodGroups {
		for i := range int(group.Spec.Replicas) {
			podName := pgs.PodName(group.Name, i)
			nodeName, ok := placements[podName]
			if !ok {
				log.Info("No placement for pod", "key", podName)
				continue
			}

			// TODO: is this really necessarry or should this case be handled somewhere else?
			existing := &corev1.Pod{}
			err := c.Get(ctx, client.ObjectKey{Name: podName, Namespace: pgs.Namespace}, existing)
			if err == nil {
				continue
			} else if client.IgnoreNotFound(err) != nil {
				return err
			}

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: pgs.Namespace,
					OwnerReferences: []metav1.OwnerReference{
						*metav1.NewControllerRef(pgs, v1alpha1.GroupVersion.WithKind("PodGroupSet")),
					},
				},
				Spec: group.Spec.PodSpec,
			}
			pod.Spec.SchedulerName = string(v1alpha1.SchedulingDomainPods)
			if err := c.Create(ctx, pod); err != nil {
				return err
			}

			// Assume that the binding succeeds and mark resources as allocated
			pod.Spec.NodeName = nodeName
			c.SCtx.Cache.AddPod(pod)

			binding := &corev1.Binding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: pgs.Namespace,
				},
				Target: corev1.ObjectReference{
					Kind: "Node",
					Name: nodeName,
				},
			}
			if err := c.Create(ctx, binding); err != nil {
				log.V(1).Error(err, "failed to assign node to pod via binding")
				c.SCtx.Cache.RemovePod(pod)

				// Add PodGroupSet to scheduling queue for retry when resources become available
				if c.SCtx.Queue != nil {
					log.Info("Adding PodGroupSet to scheduling queue due to pod binding failure", "PodGroupSet", pgs.Name, "namespace", pgs.Namespace, "pod", podName)
					c.SCtx.Queue.AddPodGroupSet(pgs)
				}

				return err
			}
			c.Recorder.Eventf(pod, nil, corev1.EventTypeNormal, "Scheduled", "SchedulePod", "Successfully assigned %s/%s to %s", pod.Namespace, pod.Name, nodeName)
			log.Info("created pod", "pod", podName, "node", nodeName)
		}
	}
	return nil
}

func (c *PodGroupSetController) handlePodGroupSet() handler.EventHandler {
	return handler.Funcs{
		CreateFunc: func(ctx context.Context, evt event.CreateEvent, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			pgs := evt.Object.(*v1alpha1.PodGroupSet)

			if err := c.ProcessNewPodGroupSet(ctx, pgs); err != nil {
				log := ctrl.LoggerFrom(ctx)
				log.Error(err, "failed to process new pgs", "pgs", pgs.Name)
			}
		},
		UpdateFunc: func(ctx context.Context, evt event.UpdateEvent, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		},
		DeleteFunc: func(ctx context.Context, evt event.DeleteEvent, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			log := ctrl.LoggerFrom(ctx)
			podgroupset := evt.Object.(*v1alpha1.PodGroupSet)

			for _, group := range podgroupset.Spec.PodGroups {
				for i := range int(group.Spec.Replicas) {
					podName := podgroupset.PodName(group.Name, i)
					pod := &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name:      podName,
							Namespace: podgroupset.Namespace,
						},
					}
					if err := c.Delete(ctx, pod); err != nil {
						if client.IgnoreNotFound(err) != nil {
							log.Error(err, "failed to delete pod for deleted podgroupset", "pod", podName)
						}
					} else {
						log.Info("deleted pod for deleted podgroupset", "pod", podName)
					}
				}
			}
		},
	}
}

func (c *PodGroupSetController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// This controller uses event handlers instead of the standard reconcile pattern
	// since PodGroupSets require immediate processing for gang scheduling
	return ctrl.Result{}, nil
}

func (c *PodGroupSetController) initPodPipeline(ctx context.Context) error {
	pipeline := &v1alpha1.Pipeline{}
	if err := c.Get(ctx, client.ObjectKey{
		Name:      "pods-scheduler",
		Namespace: "", // pipelines are cluster-scoped
	}, pipeline); err != nil {
		return fmt.Errorf("failed to get pod pipeline 'pods-scheduler': %w", err)
	}

	result := lib.InitNewFilterWeigherPipeline(ctx, c.Client, pipeline.Name, supportedFilters, pipeline.Spec.Filters, supportedWeighers, pipeline.Spec.Weighers, c.Monitor)
	if len(result.FilterErrors) > 0 || len(result.WeigherErrors) > 0 {
		return fmt.Errorf("failed to create pod pipeline: filters=%v, weighers=%v", result.FilterErrors, result.WeigherErrors)
	}

	c.podPipeline = result.Pipeline
	return nil
}

func (c *PodGroupSetController) SetupWithManager(mgr manager.Manager, mcl *multicluster.Client) error {
	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		return c.initPodPipeline(ctx)
	})); err != nil {
		return err
	}

	return multicluster.BuildController(mcl, mgr).
		WatchesMulticluster(
			&v1alpha1.PodGroupSet{},
			c.handlePodGroupSet(),
		).
		Named("cortex-podgroupset-controller").
		Complete(c)
}
