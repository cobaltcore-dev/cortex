// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/plugins/filters"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/plugins/weighers"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// FilterWeigherPipelineController owns the Nova filter-weigher pipelines: it
// watches Pipeline and Knowledge resources to (re)configure the pipelines, and
// exposes them via GetPipeline for the compute.Placement to run. Placement
// requests no longer flow through this controller — the compute.Placement,
// driven by the external-scheduler HTTP API and the reservation controllers, is
// the entrypoint.
type FilterWeigherPipelineController struct {
	// Toolbox shared between all pipeline controllers.
	lib.BasePipelineController[lib.FilterWeigherPipeline[compute.PlacementRequest]]

	// Monitor to pass down to all pipelines.
	Monitor lib.FilterWeigherPipelineMonitor
}

// The type of pipeline this controller manages.
func (c *FilterWeigherPipelineController) PipelineType() v1alpha1.PipelineType {
	return v1alpha1.PipelineTypeFilterWeigher
}

func (c *FilterWeigherPipelineController) InitPipeline(
	ctx context.Context,
	p v1alpha1.Pipeline,
) lib.PipelineInitResult[lib.FilterWeigherPipeline[compute.PlacementRequest]] {

	return lib.InitNewFilterWeigherPipeline(
		ctx, c.Client, p.Name,
		filters.Index, p.Spec.Filters,
		weighers.Index, p.Spec.Weighers,
		c.Monitor,
	)
}

func (c *FilterWeigherPipelineController) SetupWithManager(mgr manager.Manager, mcl *multicluster.Client) error {
	c.Initializer = c
	c.SchedulingDomain = v1alpha1.SchedulingDomainNova
	if err := mgr.Add(manager.RunnableFunc(c.InitAllPipelines)); err != nil {
		return err
	}
	bldr := multicluster.BuildController(mcl, mgr)
	// Watch pipeline changes so that we can reconfigure pipelines as needed.
	bldr, err := bldr.WatchesMulticluster(
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
	)
	if err != nil {
		return err
	}
	// Watch knowledge changes so that we can reconfigure pipelines as needed.
	bldr, err = bldr.WatchesMulticluster(
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
	)
	if err != nil {
		return err
	}
	// Watch hypervisor changes so the cache gets updated.
	bldr, err = bldr.WatchesMulticluster(&hv1.Hypervisor{}, handler.Funcs{})
	if err != nil {
		return err
	}
	// Watch reservation changes so the cache gets updated.
	bldr, err = bldr.WatchesMulticluster(&v1alpha1.Reservation{}, handler.Funcs{})
	if err != nil {
		return err
	}
	// Watch committed resource changes so the no-host-found classifier can read them.
	bldr, err = bldr.WatchesMulticluster(&v1alpha1.CommittedResource{}, handler.Funcs{})
	if err != nil {
		return err
	}
	return bldr.Named("cortex-nova-pipelines").
		Complete(c)
}

// Reconcile is a no-op: this controller reconfigures pipelines through its
// Pipeline/Knowledge watch handlers, and placement runs through the
// compute.Placement, not through object reconciliation. A Reconcile method is
// required to satisfy the controller builder.
func (c *FilterWeigherPipelineController) Reconcile(context.Context, ctrl.Request) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}
