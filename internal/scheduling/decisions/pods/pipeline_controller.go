// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package pods

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/api/delegation/pods"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
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
type DecisionPipelineController struct {
	// Toolbox shared between all pipeline controllers.
	lib.BasePipelineController[lib.Pipeline[pods.PodPipelineRequest]]

	// Mutex to only allow one process at a time
	processMu sync.Mutex

	// Config for the scheduling operator.
	Conf conf.Config
	// Monitor to pass down to all pipelines.
	Monitor lib.PipelineMonitor
}

// The type of pipeline this controller manages.
func (c *DecisionPipelineController) PipelineType() v1alpha1.PipelineType {
	return v1alpha1.PipelineTypeFilterWeigher
}

func (c *DecisionPipelineController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	c.processMu.Lock()
	defer c.processMu.Unlock()

	// Determine if this is a decision or pod reconciliation.
	decision := &v1alpha1.Decision{}
	if err := c.Get(ctx, req.NamespacedName, decision); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if err := c.process(ctx, decision); err != nil {
		return ctrl.Result{}, err
	}
	patch := client.MergeFrom(decision.DeepCopy())
	if err := c.Status().Patch(ctx, decision, patch); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (c *DecisionPipelineController) ProcessNewPod(ctx context.Context, pod *corev1.Pod) error {
	c.processMu.Lock()
	defer c.processMu.Unlock()

	// Create a decision resource to schedule the pod.
	decision := &v1alpha1.Decision{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "pod-",
		},
		Spec: v1alpha1.DecisionSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainPods,
			ResourceID:       pod.Name,
			PipelineRef: corev1.ObjectReference{
				Name: "pods-scheduler",
			},
			PodRef: &corev1.ObjectReference{
				Name:      pod.Name,
				Namespace: pod.Namespace,
			},
		},
	}

	pipelineConf, ok := c.PipelineConfigs[decision.Spec.PipelineRef.Name]
	if !ok {
		return fmt.Errorf("pipeline %s not configured", decision.Spec.PipelineRef.Name)
	}
	if pipelineConf.Spec.CreateDecisions {
		if err := c.Create(ctx, decision); err != nil {
			return err
		}
	}
	if err := c.process(ctx, decision); err != nil {
		return err
	}
	if pipelineConf.Spec.CreateDecisions {
		patch := client.MergeFrom(decision.DeepCopy())
		if err := c.Status().Patch(ctx, decision, patch); err != nil {
			return err
		}
	}
	return nil
}

func (c *DecisionPipelineController) process(ctx context.Context, decision *v1alpha1.Decision) error {
	log := ctrl.LoggerFrom(ctx)
	startedAt := time.Now() // So we can measure sync duration.

	pipeline, ok := c.Pipelines[decision.Spec.PipelineRef.Name]
	if !ok {
		log.Error(nil, "pipeline not found or not ready", "pipelineName", decision.Spec.PipelineRef.Name)
		return errors.New("pipeline not found or not ready")
	}

	// Find all available nodes.
	nodes := &corev1.NodeList{}
	if err := c.List(ctx, nodes); err != nil {
		return err
	}
	if len(nodes.Items) == 0 {
		return errors.New("no nodes available for scheduling")
	}

	// Execute the scheduling pipeline.
	request := pods.PodPipelineRequest{Nodes: nodes.Items}
	result, err := pipeline.Run(request)
	if err != nil {
		log.V(1).Error(err, "failed to run scheduler pipeline")
		return errors.New("failed to run scheduler pipeline")
	}
	decision.Status.Result = &result
	decision.Status.Took = metav1.Duration{Duration: time.Since(startedAt)}
	log.Info("decision processed successfully", "duration", time.Since(startedAt))

	// Check if the pod is already assigned to a node.
	pod := &corev1.Pod{}
	if err := c.Get(ctx, client.ObjectKey{
		Name:      decision.Spec.PodRef.Name,
		Namespace: decision.Spec.PodRef.Namespace,
	}, pod); err != nil {
		log.Error(err, "failed to fetch pod for decision")
		return err
	}
	if pod.Spec.NodeName != "" {
		log.Info("pod is already assigned to a node", "node", pod.Spec.NodeName)
		return nil
	}

	// Assign the first node returned by the pipeline using a Binding.
	binding := &corev1.Binding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      decision.Spec.PodRef.Name,
			Namespace: decision.Spec.PodRef.Namespace,
		},
		Target: corev1.ObjectReference{
			Kind: "Node",
			Name: *result.TargetHost,
		},
	}
	if err := c.Create(ctx, binding); err != nil {
		log.V(1).Error(err, "failed to assign node to pod via binding")
		return err
	}
	log.V(1).Info("assigned node to pod", "node", *result.TargetHost)
	return nil
}

// The base controller will delegate the pipeline creation down to this method.
func (c *DecisionPipelineController) InitPipeline(
	ctx context.Context,
	name string,
	steps []v1alpha1.Step,
) (lib.Pipeline[pods.PodPipelineRequest], error) {

	return lib.NewPipeline(ctx, c.Client, name, supportedSteps, steps, c.Monitor)
}

func (c *DecisionPipelineController) handlePod() handler.EventHandler {
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
				if decision.Spec.PodRef.Name == pod.Name && decision.Spec.PodRef.Namespace == pod.Namespace {
					if err := c.Delete(ctx, &decision); err != nil {
						log.Error(err, "failed to delete decision for deleted pod")
					}
				}
			}
		},
	}
}

func (c *DecisionPipelineController) SetupWithManager(mgr manager.Manager, mcl *multicluster.Client) error {
	c.Initializer = c
	c.SchedulingDomain = v1alpha1.SchedulingDomainPods
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
		// Watch pipeline changes so that we can reconfigure pipelines as needed.
		WatchesMulticluster(
			&v1alpha1.Pipeline{},
			handler.Funcs{
				CreateFunc: c.HandlePipelineCreated,
				UpdateFunc: c.HandlePipelineUpdated,
				DeleteFunc: c.HandlePipelineDeleted,
			},
			predicate.NewPredicateFuncs(func(obj client.Object) bool {
				pipeline := obj.(*v1alpha1.Pipeline)
				// Only react to pipelines matching the scheduling domain.
				if pipeline.Spec.SchedulingDomain != v1alpha1.SchedulingDomainPods {
					return false
				}
				return pipeline.Spec.Type == v1alpha1.PipelineTypeFilterWeigher
			}),
		).
		// Watch step changes so that we can turn on/off pipelines depending on
		// unready steps.
		WatchesMulticluster(
			&v1alpha1.Step{},
			handler.Funcs{
				CreateFunc: c.HandleStepCreated,
				UpdateFunc: c.HandleStepUpdated,
				DeleteFunc: c.HandleStepDeleted,
			},
			predicate.NewPredicateFuncs(func(obj client.Object) bool {
				step := obj.(*v1alpha1.Step)
				// Only react to steps matching the scheduling domain.
				if step.Spec.SchedulingDomain != v1alpha1.SchedulingDomainPods {
					return false
				}
				// Only react to filter and weigher steps.
				supportedTypes := []v1alpha1.StepType{
					v1alpha1.StepTypeFilter,
					v1alpha1.StepTypeWeigher,
				}
				return slices.Contains(supportedTypes, step.Spec.Type)
			}),
		).
		Named("cortex-pod-scheduler").
		For(
			&v1alpha1.Decision{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				decision := obj.(*v1alpha1.Decision)
				if decision.Spec.SchedulingDomain != v1alpha1.SchedulingDomainPods {
					return false
				}
				// Ignore already decided schedulings.
				if decision.Status.Result != nil {
					return false
				}
				return true
			})),
		).
		Complete(c)
}
