// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package pods

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/api/external/pods"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/pods/plugins/filters"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/pods/plugins/weighers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// The decision pipeline controller takes decision resources containing a
// pod ref and runs the scheduling pipeline to make a decision.
// This decision is then written back to the decision resource status.
//
// Additionally, the controller watches for pipeline and step changes to
// reconfigure the pipelines as needed.
type FilterWeigherPipelineController struct {
	// Toolbox shared between all pipeline controllers.
	lib.BasePipelineController[lib.FilterWeigherPipeline[pods.PodPipelineRequest]]

	// Mutex to only allow one process at a time
	processMu sync.Mutex

	// Monitor to pass down to all pipelines.
	Monitor lib.FilterWeigherPipelineMonitor
}

// The type of pipeline this controller manages.
func (c *FilterWeigherPipelineController) PipelineType() v1alpha1.PipelineType {
	return v1alpha1.PipelineTypeFilterWeigher
}

func (c *FilterWeigherPipelineController) ProcessNewPod(ctx context.Context, pod *corev1.Pod) error {
	c.processMu.Lock()
	defer c.processMu.Unlock()

	log := ctrl.LoggerFrom(ctx)
	startedAt := time.Now()

	pipelineName := "pods-scheduler"

	pipeline, ok := c.Pipelines[pipelineName]
	if !ok {
		return fmt.Errorf("pipeline %s not found or not ready", pipelineName)
	}

	pipelineConfig, ok := c.PipelineConfigs[pipelineName]
	if !ok {
		return fmt.Errorf("pipeline %s not configured", pipelineName)
	}

	if pod.Spec.NodeName != "" {
		log.Info("pod is already assigned to a node", "node", pod.Spec.NodeName)
		return nil
	}

	nodes := &corev1.NodeList{}
	if err := c.List(ctx, nodes); err != nil {
		return err
	}
	if len(nodes.Items) == 0 {
		return errors.New("no nodes available for scheduling")
	}

	request := pods.PodPipelineRequest{Nodes: nodes.Items, Pod: *pod}
	result, err := pipeline.Run(request)
	if err != nil {
		log.V(1).Error(err, "failed to run scheduler pipeline")
		return errors.New("failed to run scheduler pipeline")
	}

	log.Info("pod processed successfully", "duration", time.Since(startedAt))

	hosts := result.OrderedHosts
	if len(hosts) == 0 {
		log.Info("no suitable nodes found for pod")
		return nil
	}

	targetHost := hosts[0]

	binding := &corev1.Binding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
		Target: corev1.ObjectReference{
			Kind: "Node",
			Name: targetHost,
		},
	}
	if err := c.Create(ctx, binding); err != nil {
		log.V(1).Error(err, "failed to assign node to pod via binding")
		return err
	}
	log.V(1).Info("assigned node to pod", "node", targetHost)

	if pipelineConfig.Spec.CreateDecisions {
		c.DecisionQueue <- lib.DecisionUpdate{
			ResourceID:   pod.Name,
			PipelineName: pipelineName,
			Result:       result,
			// TODO: Refine the reason
			Reason: v1alpha1.SchedulingIntentUnknown,
		}
	}
	return nil
}

// The base controller will delegate the pipeline creation down to this method.
func (c *FilterWeigherPipelineController) InitPipeline(
	ctx context.Context,
	p v1alpha1.Pipeline,
) lib.PipelineInitResult[lib.FilterWeigherPipeline[pods.PodPipelineRequest]] {

	return lib.InitNewFilterWeigherPipeline(
		ctx, c.Client, p.Name,
		filters.Index, p.Spec.Filters,
		weighers.Index, p.Spec.Weighers,
		c.Monitor,
	)
}

func (c *FilterWeigherPipelineController) handlePod() handler.EventHandler {
	return handler.Funcs{
		CreateFunc: func(ctx context.Context, evt event.CreateEvent, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			pod := evt.Object.(*corev1.Pod)
			if err := c.ProcessNewPod(ctx, pod); err != nil {
				log := ctrl.LoggerFrom(ctx)
				log.Error(err, "failed to process new pod for scheduling", "pod", pod.Name)
			}
		},
		UpdateFunc: func(ctx context.Context, evt event.UpdateEvent, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			newPod := evt.ObjectNew.(*corev1.Pod)
			if newPod.Spec.NodeName != "" {
				// Pod is already scheduled, no need to create a decision.
				return
			}
			if err := c.ProcessNewPod(ctx, newPod); err != nil {
				log := ctrl.LoggerFrom(ctx)
				log.Error(err, "failed to process new pod for scheduling", "pod", newPod.Name)
			}
		},
		DeleteFunc: func(ctx context.Context, evt event.DeleteEvent, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			// Delete the associated decision(s).
			log := ctrl.LoggerFrom(ctx)
			pod := evt.Object.(*corev1.Pod)
			var decisions v1alpha1.DecisionList
			if err := c.List(ctx, &decisions); err != nil {
				log.Error(err, "failed to list decisions for deleted pod")
				return
			}
			for _, decision := range decisions.Items {
				if decision.Spec.ResourceID == pod.Name && decision.Spec.SchedulingDomain == v1alpha1.SchedulingDomainPods {
					if err := c.Delete(ctx, &decision); err != nil {
						log.Error(err, "failed to delete decision for deleted pod")
					}
				}
			}
		},
	}
}

func (c *FilterWeigherPipelineController) SetupWithManager(mgr manager.Manager, mcl *multicluster.Client) error {
	c.Initializer = c
	c.SchedulingDomain = v1alpha1.SchedulingDomainPods
	c.Recorder = mgr.GetEventRecorder("cortex-pods-pipeline-controller")
	if err := mgr.Add(manager.RunnableFunc(c.InitAllPipelines)); err != nil {
		return err
	}
	return multicluster.BuildController(mcl, mgr).
		WatchesMulticluster(
			&corev1.Pod{},
			c.handlePod(),
			// Only schedule pods that have a custom scheduler set.
			predicate.NewPredicateFuncs(func(obj client.Object) bool {
				pod := obj.(*corev1.Pod)
				if pod.Spec.NodeName != "" {
					// Skip pods that already have a node assigned.
					return false
				}
				return pod.Spec.SchedulerName == string(v1alpha1.SchedulingDomainPods)
			}),
		).
		Named("cortex-pod-scheduler").
		For(
			&v1alpha1.Pipeline{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				pipeline := obj.(*v1alpha1.Pipeline)
				if pipeline.Spec.SchedulingDomain != v1alpha1.SchedulingDomainPods {
					return false
				}
				return pipeline.Spec.Type == c.PipelineType()
			})),
		).
		Complete(c)
}
