// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"log/slog"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/plugins"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/plugins/detectors"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	"github.com/sapcc/go-bits/jobloop"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// The deschedulings pipeline controller is responsible for periodically running
// the descheduling pipeline and creating descheduling resources based on the
// selections made.
//
// Additionally, the controller watches for pipeline and step changes to
// reconfigure the pipelines as needed.
type DetectorPipelineController struct {
	// Toolbox shared between all pipeline controllers.
	lib.BasePipelineController[*lib.DetectorPipeline[plugins.VMDetection]]

	// Monitor to pass down to all pipelines.
	Monitor lib.DetectorPipelineMonitor
	// Cycle detector to avoid descheduling loops.
	Breaker lib.DetectorCycleBreaker[plugins.VMDetection]
}

// The type of pipeline this controller manages.
func (c *DetectorPipelineController) PipelineType() v1alpha1.PipelineType {
	return v1alpha1.PipelineTypeDetector
}

// The base controller will delegate the pipeline creation down to this method.
func (c *DetectorPipelineController) InitPipeline(
	ctx context.Context,
	p v1alpha1.Pipeline,
) lib.PipelineInitResult[*lib.DetectorPipeline[plugins.VMDetection]] {

	pipeline := &lib.DetectorPipeline[plugins.VMDetection]{
		Client:  c.Client,
		Breaker: c.Breaker,
		Monitor: c.Monitor.SubPipeline(p.Name),
	}
	errs := pipeline.Init(ctx, p.Spec.Detectors, detectors.Index)
	return lib.PipelineInitResult[*lib.DetectorPipeline[plugins.VMDetection]]{
		Pipeline:       pipeline,
		DetectorErrors: errs,
	}
}

func (c *DetectorPipelineController) CreateDeschedulingsPeriodically(ctx context.Context) {
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
			decisions, err = p.Breaker.Filter(ctx, decisions)
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

func (c *DetectorPipelineController) SetupWithManager(mgr ctrl.Manager, mcl *multicluster.Client) error {
	c.Initializer = c
	c.SchedulingDomain = v1alpha1.SchedulingDomainNova
	if err := mgr.Add(manager.RunnableFunc(c.InitAllPipelines)); err != nil {
		return err
	}
	return multicluster.BuildController(mcl, mgr).
		// Watch pipeline changes so that we can reconfigure pipelines as needed.
		WatchesMulticluster(
			&v1alpha1.Knowledge{},
			// Get all pipelines of the controller when knowledge changes and trigger reconciliation to update the candidates in the pipelines.
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				knowledge := obj.(*v1alpha1.Knowledge)
				if knowledge.Spec.SchedulingDomain != v1alpha1.SchedulingDomainNova {
					return nil
				}
				// When Knowledge changes, reconcile all pipelines
				return c.GetAllPipelineReconcileRequests(ctx)
			}),
			predicate.NewPredicateFuncs(func(obj client.Object) bool {
				knowledge := obj.(*v1alpha1.Knowledge)
				// Only react to knowledge matching the scheduling domain.
				return knowledge.Spec.SchedulingDomain == v1alpha1.SchedulingDomainNova
			}),
		).
		// Watch hypervisor changes so the cache gets updated.
		Named("cortex-nova-pipelines").
		For(
			&v1alpha1.Pipeline{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				pipeline := obj.(*v1alpha1.Pipeline)
				if pipeline.Spec.SchedulingDomain != v1alpha1.SchedulingDomainNova {
					return false
				}
				return pipeline.Spec.Type == c.PipelineType()
			})),
		).
		Complete(c)
}
