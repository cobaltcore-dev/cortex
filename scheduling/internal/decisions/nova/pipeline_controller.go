// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"

	knowledgev1alpha1 "github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/lib/db"
	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/conf"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/lib"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type pendingRequest struct {
	responseChan chan *v1alpha1.Decision
	cancelChan   chan struct{}
}

// The decision pipeline controller takes decision resources containing a
// placement request spec and runs the scheduling pipeline to make a decision.
// This decision is then written back to the decision resource status.
//
// Additionally, the controller watches for pipeline and step changes to
// reconfigure the pipelines as needed.
type DecisionPipelineController struct {
	// Toolbox shared between all pipeline controllers.
	lib.BasePipelineController[lib.Pipeline[api.ExternalSchedulerRequest]]

	// Map of pending API requests by decision key (namespace/name)
	pendingRequests map[string]*pendingRequest
	// Mutex to protect concurrent access to pendingRequests
	mu sync.RWMutex

	// Database to pass down to all steps.
	DB db.DB
	// Monitor to pass down to all pipelines.
	Monitor lib.PipelineMonitor
	// Config for the scheduling operator.
	Conf conf.Config
}

func (c *DecisionPipelineController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startedAt := time.Now() // So we can measure sync duration.
	log := ctrl.LoggerFrom(ctx)

	decision := &v1alpha1.Decision{}
	if err := c.Get(ctx, req.NamespacedName, decision); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	pipeline, ok := c.Pipelines[decision.Spec.PipelineRef.Name]
	if !ok {
		log.Error(nil, "pipeline not found or not ready", "pipelineName", decision.Spec.PipelineRef.Name)
		return ctrl.Result{}, nil
	}
	if decision.Spec.NovaRaw == nil {
		log.Info("skipping decision, no novaRaw spec defined")
		return ctrl.Result{}, nil
	}
	var request api.ExternalSchedulerRequest
	if err := json.Unmarshal(decision.Spec.NovaRaw.Raw, &request); err != nil {
		log.Error(err, "failed to unmarshal novaRaw spec")
		return ctrl.Result{}, err
	}

	result, err := pipeline.Run(request)
	if err != nil {
		log.Error(err, "failed to run pipeline")
		return ctrl.Result{}, err
	}
	decision.Status.Result = &result
	decision.Status.Took = metav1.Duration{Duration: time.Since(startedAt)}
	if err := c.Status().Update(ctx, decision); err != nil {
		log.Error(err, "failed to update decision status")
		return ctrl.Result{}, err
	}
	log.Info("decision processed successfully", "duration", time.Since(startedAt))

	// Check if there's a pending request waiting for this decision
	decisionKey := req.Namespace + "/" + req.Name
	c.mu.RLock()
	pending, exists := c.pendingRequests[decisionKey]
	c.mu.RUnlock()

	if exists {
		// Send the decision to the waiting API request
		select {
		case pending.responseChan <- decision:
			log.Info("sent decision response to pending API request", "decisionKey", decisionKey)
		case <-pending.cancelChan:
			log.Info("pending request was cancelled", "decisionKey", decisionKey)
		default:
			log.Info("no receiver for decision response", "decisionKey", decisionKey)
		}
	}

	return ctrl.Result{}, nil
}

// The base controller will delegate the pipeline creation down to this method.
func (c *DecisionPipelineController) InitPipeline(steps []v1alpha1.Step) (lib.Pipeline[api.ExternalSchedulerRequest], error) {
	return NewPipeline(steps, c.DB, c.Monitor)
}

// Process the decision from the API. Should create and return the updated decision.
func (c *DecisionPipelineController) ProcessNewDecisionFromAPI(ctx context.Context, decision *v1alpha1.Decision) (*v1alpha1.Decision, error) {
	// Create the decision object in kubernetes first
	if err := c.Create(ctx, decision); err != nil {
		return nil, err
	}

	// Create a pending request entry
	decisionKey := decision.Namespace + "/" + decision.Name
	pending := &pendingRequest{
		responseChan: make(chan *v1alpha1.Decision, 1),
		cancelChan:   make(chan struct{}),
	}
	c.mu.Lock()
	if c.pendingRequests == nil {
		c.pendingRequests = make(map[string]*pendingRequest)
	}
	c.pendingRequests[decisionKey] = pending
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pendingRequests, decisionKey)
		c.mu.Unlock()
		close(pending.cancelChan)
	}()
	select {
	case updatedDecision := <-pending.responseChan:
		return updatedDecision, nil
	case <-time.After(30 * time.Second):
		return nil, context.DeadlineExceeded
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *DecisionPipelineController) SetupWithManager(mgr manager.Manager) error {
	c.BasePipelineController.Delegate = c
	// Initialize the pending requests map
	c.pendingRequests = make(map[string]*pendingRequest)
	if err := mgr.Add(manager.RunnableFunc(c.InitAllPipelines)); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		Named("cortex-nova-decisions").
		For(
			&v1alpha1.Decision{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				decision := obj.(*v1alpha1.Decision)
				if decision.Spec.Operator != c.Conf.Operator {
					return false
				}
				// Ignore already decided schedulings.
				if decision.Status.Result != nil {
					return false
				}
				// Only handle nova decisions.
				return decision.Spec.Type == v1alpha1.DecisionTypeNovaServer
			})),
		).
		// Watch pipeline changes so that we can reconfigure pipelines as needed.
		Watches(
			&v1alpha1.Pipeline{},
			handler.Funcs{
				CreateFunc: c.HandlePipelineCreated,
				UpdateFunc: c.HandlePipelineUpdated,
				DeleteFunc: c.HandlePipelineDeleted,
			},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				pipeline := obj.(*v1alpha1.Pipeline)
				// Only react to pipelines matching the operator.
				if pipeline.Spec.Operator != c.Conf.Operator {
					return false
				}
				return pipeline.Spec.Type == v1alpha1.PipelineTypeFilterWeigher
			})),
		).
		// Watch step changes so that we can turn on/off pipelines depending on
		// unready steps.
		Watches(
			&v1alpha1.Step{},
			handler.Funcs{
				CreateFunc: c.HandleStepCreated,
				UpdateFunc: c.HandleStepUpdated,
				DeleteFunc: c.HandleStepDeleted,
			},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				step := obj.(*v1alpha1.Step)
				// Only react to steps matching the operator.
				if step.Spec.Operator != c.Conf.Operator {
					return false
				}
				// Only react to filter and weigher steps.
				supportedTypes := []v1alpha1.StepType{
					v1alpha1.StepTypeFilter,
					v1alpha1.StepTypeWeigher,
				}
				return slices.Contains(supportedTypes, step.Spec.Type)
			})),
		).
		// Watch knowledge changes so that we can reconfigure pipelines as needed.
		Watches(
			&knowledgev1alpha1.Knowledge{},
			handler.Funcs{
				CreateFunc: c.HandleKnowledgeCreated,
				UpdateFunc: c.HandleKnowledgeUpdated,
				DeleteFunc: c.HandleKnowledgeDeleted,
			},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				knowledge := obj.(*knowledgev1alpha1.Knowledge)
				// Only react to knowledge matching the operator.
				return knowledge.Spec.Operator == c.Conf.Operator
			})),
		).
		Complete(c)
}
