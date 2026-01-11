// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package podgroupsets

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/api/delegation/podgroupsets"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
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

type DecisionPipelineController struct {
	// Toolbox shared between all pipeline controllers.
	lib.BasePipelineController[lib.Pipeline[podgroupsets.PodGroupSetPipelineRequest]]

	// Mutex to only allow one process at a time
	processMu sync.Mutex

	// Config for the scheduling operator.
	Conf conf.Config

	// Monitor to pass down to all pipelines.
	Monitor lib.PipelineMonitor
}

// The type of pipeline this controller manages.
func (c *DecisionPipelineController) PipelineType() v1alpha1.PipelineType {
	return v1alpha1.PipelineTypeGang
}

func (c *DecisionPipelineController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	c.processMu.Lock()
	defer c.processMu.Unlock()

	decision := &v1alpha1.Decision{}
	if err := c.Get(ctx, req.NamespacedName, decision); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	old := decision.DeepCopy()
	if err := c.process(ctx, decision); err != nil {
		return ctrl.Result{}, err
	}
	patch := client.MergeFrom(old)
	if err := c.Status().Patch(ctx, decision, patch); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (c *DecisionPipelineController) ProcessNewPodGroupSet(ctx context.Context, pgs *v1alpha1.PodGroupSet) error {
	c.processMu.Lock()
	defer c.processMu.Unlock()

	decision := &v1alpha1.Decision{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "podgroupset-",
		},
		Spec: v1alpha1.DecisionSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainPodGroupSets,
			ResourceID:       pgs.Name,
			PipelineRef: corev1.ObjectReference{
				Name: "podgroupsets-scheduler",
			},
			PodGroupSetRef: &corev1.ObjectReference{
				Name:      pgs.Name,
				Namespace: pgs.Namespace,
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

	old := decision.DeepCopy()
	err := c.process(ctx, decision)
	if err != nil {
		meta.SetStatusCondition(&decision.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DecisionConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "PipelineRunFailed",
			Message: "pipeline run failed: " + err.Error(),
		})
	} else {
		meta.SetStatusCondition(&decision.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DecisionConditionReady,
			Status:  metav1.ConditionTrue,
			Reason:  "PipelineRunSucceeded",
			Message: "pipeline run succeeded",
		})
	}
	if pipelineConf.Spec.CreateDecisions {
		patch := client.MergeFrom(old)
		if err := c.Status().Patch(ctx, decision, patch); err != nil {
			return err
		}
	}
	return err
}

func (c *DecisionPipelineController) process(ctx context.Context, decision *v1alpha1.Decision) error {
	log := ctrl.LoggerFrom(ctx)
	startedAt := time.Now()

	pipeline, ok := c.Pipelines[decision.Spec.PipelineRef.Name]
	if !ok {
		log.Error(nil, "pipeline not found or not ready", "pipelineName", decision.Spec.PipelineRef.Name)
		return errors.New("pipeline not found or not ready")
	}

	// Fetch PodGroupSet
	podGroupSet := &v1alpha1.PodGroupSet{}
	if err := c.Get(ctx, client.ObjectKey{
		Name:      decision.Spec.PodGroupSetRef.Name,
		Namespace: decision.Spec.PodGroupSetRef.Namespace,
	}, podGroupSet); err != nil {
		return err
	}

	if decision.Status.Result == nil {
		// Find nodes
		nodes := &corev1.NodeList{}
		if err := c.List(ctx, nodes); err != nil {
			return err
		}
		if len(nodes.Items) == 0 {
			return errors.New("no nodes available")
		}

		// Run pipeline
		request := podgroupsets.PodGroupSetPipelineRequest{
			PodGroupSet: *podGroupSet,
			Nodes:       nodes.Items,
		}
		result, err := pipeline.Run(request)
		if err != nil {
			log.V(1).Error(err, "pipeline run failed")
			return errors.New("pipeline run failed: " + err.Error())
		}
		decision.Status.Result = &result
		log.Info("decision processed", "duration", time.Since(startedAt))
	}

	// Spawn Pods
	if decision.Status.Result != nil && decision.Status.Result.TargetPlacements != nil {
		for _, group := range podGroupSet.Spec.PodGroups {
			for i := 0; i < int(group.Spec.Replicas); i++ {
				podKey := fmt.Sprintf("%s-%d", group.Name, i)
				nodeName, ok := decision.Status.Result.TargetPlacements[podKey]
				if !ok {
					log.Info("No placement for pod", "key", podKey)
					continue
				}

				podName := fmt.Sprintf("%s-%s-%d", podGroupSet.Name, group.Name, i)

				// Check if pod exists
				existing := &corev1.Pod{}
				err := c.Get(ctx, client.ObjectKey{Name: podName, Namespace: podGroupSet.Namespace}, existing)
				if err == nil {
					// exists
					continue
				} else if client.IgnoreNotFound(err) != nil {
					return err
				}

				// Create pod
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      podName,
						Namespace: podGroupSet.Namespace,
						OwnerReferences: []metav1.OwnerReference{
							*metav1.NewControllerRef(podGroupSet, v1alpha1.GroupVersion.WithKind("PodGroupSet")),
						},
					},
					Spec: group.Spec.PodSpec,
				}
				// Bind
				pod.Spec.NodeName = nodeName

				if err := c.Create(ctx, pod); err != nil {
					return err
				}
				log.Info("created pod", "pod", podName, "node", nodeName)
			}
		}
	}

	return nil
}

func (c *DecisionPipelineController) InitPipeline(
	ctx context.Context,
	p v1alpha1.Pipeline,
) (lib.Pipeline[podgroupsets.PodGroupSetPipelineRequest], error) {
	return lib.NewPipeline(ctx, c.Client, p.Name, supportedSteps, p.Spec.Steps, c.Monitor)
}

func (c *DecisionPipelineController) handlePodGroupSet() handler.EventHandler {
	return handler.Funcs{
		CreateFunc: func(ctx context.Context, evt event.CreateEvent, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			pgs := evt.Object.(*v1alpha1.PodGroupSet)
			if err := c.ProcessNewPodGroupSet(ctx, pgs); err != nil {
				log := ctrl.LoggerFrom(ctx)
				log.Error(err, "failed to process new pgs", "pgs", pgs.Name)
			}
		},
		UpdateFunc: func(ctx context.Context, evt event.UpdateEvent, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			newPodGroupSet := evt.ObjectNew.(*v1alpha1.PodGroupSet)
			if err := c.ProcessNewPodGroupSet(ctx, newPodGroupSet); err != nil {
				log := ctrl.LoggerFrom(ctx)
				log.Error(err, "failed to process updated pgs", "pgs", newPodGroupSet.Name)
			}
		},
		DeleteFunc: func(ctx context.Context, evt event.DeleteEvent, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			log := ctrl.LoggerFrom(ctx)
			podgroupset := evt.Object.(*v1alpha1.PodGroupSet)
			var decisions v1alpha1.DecisionList
			if err := c.List(ctx, &decisions); err != nil {
				log.Error(err, "failed to list decisions for deleted podgroupset")
				return
			}
			for _, decision := range decisions.Items {
				if decision.Spec.PodGroupSetRef != nil &&
					decision.Spec.PodGroupSetRef.Name == podgroupset.Name &&
					decision.Spec.PodGroupSetRef.Namespace == podgroupset.Namespace {
					if err := c.Delete(ctx, &decision); err != nil {
						// log error
					}
				}
			}
		},
	}
}

func (c *DecisionPipelineController) SetupWithManager(mgr manager.Manager, mcl *multicluster.Client) error {
	c.Initializer = c
	c.SchedulingDomain = v1alpha1.SchedulingDomainPodGroupSets
	if err := mgr.Add(manager.RunnableFunc(c.InitAllPipelines)); err != nil {
		return err
	}
	return multicluster.BuildController(mcl, mgr).
		WatchesMulticluster(
			&v1alpha1.PodGroupSet{},
			c.handlePodGroupSet(),
			predicate.NewPredicateFuncs(func(obj client.Object) bool {
				// We can add logic here to filter out PodGroupSets that don't need scheduling.
				return true
			}),
		).
		WatchesMulticluster(
			&v1alpha1.Pipeline{},
			handler.Funcs{
				CreateFunc: c.HandlePipelineCreated,
				UpdateFunc: c.HandlePipelineUpdated,
				DeleteFunc: c.HandlePipelineDeleted,
			},
			predicate.NewPredicateFuncs(func(obj client.Object) bool {
				pipeline := obj.(*v1alpha1.Pipeline)
				if pipeline.Spec.SchedulingDomain != v1alpha1.SchedulingDomainPodGroupSets {
					return false
				}
				return pipeline.Spec.Type == v1alpha1.PipelineTypeGang
			}),
		).
		Named("cortex-podgroupset-scheduler").
		For(
			&v1alpha1.Decision{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				decision := obj.(*v1alpha1.Decision)
				if decision.Spec.SchedulingDomain != v1alpha1.SchedulingDomainPodGroupSets {
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
