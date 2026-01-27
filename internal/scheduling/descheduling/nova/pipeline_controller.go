// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"log/slog"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/descheduling/nova/plugins"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	"github.com/sapcc/go-bits/jobloop"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// The deschedulings pipeline controller is responsible for periodically running
// the descheduling pipeline and creating descheduling resources based on the
// selections made.
//
// Additionally, the controller watches for pipeline and step changes to
// reconfigure the pipelines as needed.
type DeschedulingsPipelineController struct {
	// Toolbox shared between all pipeline controllers.
	lib.BasePipelineController[*lib.DetectorPipeline[plugins.VMDetection]]

	// Monitor to pass down to all pipelines.
	Monitor lib.DetectorPipelineMonitor
	// Config for the scheduling operator.
	Conf conf.Config
	// Cycle detector to avoid descheduling loops.
	CycleBreaker lib.CycleBreaker[plugins.VMDetection]
}

// The type of pipeline this controller manages.
func (c *DeschedulingsPipelineController) PipelineType() v1alpha1.PipelineType {
	return v1alpha1.PipelineTypeDescheduler
}

// The base controller will delegate the pipeline creation down to this method.
func (c *DeschedulingsPipelineController) InitPipeline(
	ctx context.Context,
	p v1alpha1.Pipeline,
) lib.PipelineInitResult[*lib.DetectorPipeline[plugins.VMDetection]] {

	pipeline := &lib.DetectorPipeline[plugins.VMDetection]{
		Client:       c.Client,
		CycleBreaker: c.CycleBreaker,
		Monitor:      c.Monitor.SubPipeline(p.Name),
	}
	nonCriticalErr, criticalErr := pipeline.Init(ctx, p.Spec.Detectors, supportedDetectors)
	return lib.PipelineInitResult[*lib.DetectorPipeline[plugins.VMDetection]]{
		Pipeline:       pipeline,
		NonCriticalErr: nonCriticalErr,
		CriticalErr:    criticalErr,
	}
}

func (c *DeschedulingsPipelineController) CreateDeschedulingsPeriodically(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			slog.Info("descheduler shutting down")
			return
		default:
			// Get the pipeline for the current configuration.
			p, ok := c.Pipelines["nova-descheduler-kvm"]
			if !ok {
				slog.Error("descheduler: pipeline not found or not ready yet")
				time.Sleep(jobloop.DefaultJitter(time.Minute))
				continue
			}
			decisionsByStep := p.Run()
			if len(decisionsByStep) == 0 {
				slog.Info("descheduler: no decisions made in this run")
				time.Sleep(jobloop.DefaultJitter(time.Minute))
				continue
			}
			slog.Info("descheduler: decisions made", "decisionsByStep", decisionsByStep)
			decisions := p.Combine(decisionsByStep)
			var err error
			decisions, err = p.CycleBreaker.Filter(ctx, decisions)
			if err != nil {
				slog.Error("descheduler: failed to filter decisions for cycles", "error", err)
				time.Sleep(jobloop.DefaultJitter(time.Minute))
				continue
			}
			for _, decision := range decisions {
				// Precaution: If a descheduling for the VM already exists, skip it.
				// The TTL controller will clean up old deschedulings so the vm
				// can be descheduled again later if needed, or we can manually
				// delete the descheduling if we want to deschedule the VM again.
				var existing v1alpha1.Descheduling
				err := p.Get(ctx, client.ObjectKey{Name: decision.VMID}, &existing)
				if err == nil {
					slog.Info("descheduler: descheduling already exists for VM, skipping", "vmId", decision.VMID)
					continue
				}

				descheduling := &v1alpha1.Descheduling{}
				descheduling.Name = decision.VMID
				descheduling.Spec.Ref = decision.VMID
				descheduling.Spec.RefType = v1alpha1.DeschedulingSpecVMReferenceNovaServerUUID
				descheduling.Spec.PrevHostType = v1alpha1.DeschedulingSpecHostTypeNovaComputeHostName
				descheduling.Spec.PrevHost = decision.Host
				descheduling.Spec.Reason = decision.Reason
				if err := p.Create(ctx, descheduling); err != nil {
					slog.Error("descheduler: failed to create descheduling", "error", err)
					time.Sleep(jobloop.DefaultJitter(time.Minute))
					continue
				}
				slog.Info("descheduler: created descheduling", "vmId", decision.VMID, "host", decision.Host, "reason", decision.Reason)
			}

			time.Sleep(jobloop.DefaultJitter(time.Minute))
		}
	}
}

func (c *DeschedulingsPipelineController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// This controller does not reconcile any resources directly.
	return ctrl.Result{}, nil
}

func (c *DeschedulingsPipelineController) SetupWithManager(mgr ctrl.Manager, mcl *multicluster.Client) error {
	c.Initializer = c
	c.SchedulingDomain = v1alpha1.SchedulingDomainNova
	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		// Initialize the cycle detector.
		return c.CycleBreaker.Init(ctx, mgr.GetClient(), c.Conf)
	})); err != nil {
		return err
	}
	if err := mgr.Add(manager.RunnableFunc(c.InitAllPipelines)); err != nil {
		return err
	}
	return multicluster.BuildController(mcl, mgr).
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
				if pipeline.Spec.SchedulingDomain != v1alpha1.SchedulingDomainNova {
					return false
				}
				return pipeline.Spec.Type == c.PipelineType()
			}),
		).
		// Watch knowledge changes so that we can reconfigure pipelines as needed.
		WatchesMulticluster(
			&v1alpha1.Knowledge{},
			handler.Funcs{
				CreateFunc: c.HandleKnowledgeCreated,
				UpdateFunc: c.HandleKnowledgeUpdated,
				DeleteFunc: c.HandleKnowledgeDeleted,
			},
			predicate.NewPredicateFuncs(func(obj client.Object) bool {
				knowledge := obj.(*v1alpha1.Knowledge)
				// Only react to knowledge matching the scheduling domain.
				return knowledge.Spec.SchedulingDomain == v1alpha1.SchedulingDomainNova
			}),
		).
		Named("cortex-nova-deschedulings").
		For(
			&v1alpha1.Descheduling{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				return false // This controller does not reconcile Descheduling resources directly.
			})),
		).
		Complete(c)
}
