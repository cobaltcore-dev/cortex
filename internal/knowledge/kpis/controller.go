// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package kpis

import (
	"context"
	"errors"
	"fmt"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
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
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// The kpi controller checks the status of kpi dependencies and populates
// the kpi status accordingly.
type Controller struct {
	// Kubernetes client to manage/fetch resources.
	client.Client
	// The scheduling domain to scope resources to.
	SchedulingDomain v1alpha1.SchedulingDomain

	// The supported kpis to manage.
	supportedKPIs map[string]plugins.KPI
	// Registered kpis by name.
	registeredKPIsByResourceName map[string]plugins.KPI
}

// This loop will be called by the controller-runtime for each kpi
// resource that needs to be reconciled.
func (c *Controller) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	kpi := &v1alpha1.KPI{}

	if err := c.Get(ctx, req.NamespacedName, kpi); err != nil {
		// Remove the kpi if it was deleted.
		if client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}
		var kpis v1alpha1.KPIList
		if err := c.List(ctx, &kpis); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to list kpis: %w", err)
		}
		if existingKPI, ok := c.registeredKPIsByResourceName[req.Name]; ok {
			metrics.Registry.Unregister(existingKPI)
			delete(c.registeredKPIsByResourceName, req.Name)
			log.Info("kpi: unregistered deleted kpi", "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, nil
	}

	// If this kpi is not supported, ignore it.
	if _, ok := c.supportedKPIs[kpi.Spec.Impl]; !ok {
		log.Info("kpi: unsupported kpi, ignoring", "name", req.Name)
		return ctrl.Result{}, nil
	}

	// Reconcile the kpi.
	old := kpi.DeepCopy()
	err := c.handleKPIChange(ctx, kpi)
	if err != nil {
		meta.SetStatusCondition(&kpi.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.KPIConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "ReconciliationFailed",
			Message: err.Error(),
		})
	} else {
		meta.SetStatusCondition(&kpi.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.KPIConditionReady,
			Status:  metav1.ConditionTrue,
			Reason:  "ReconciliationSucceeded",
			Message: "kpi reconciled successfully",
		})
	}
	patch := client.MergeFrom(old)
	if err := c.Status().Patch(ctx, kpi, patch); err != nil {
		log.Error(err, "failed to patch kpi status after reconciliation error", "name", kpi.Name)
	}
	return ctrl.Result{}, nil
}

// Handle the startup of the manager and initialize the kpis to be used.
func (c *Controller) InitAllKPIs(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("initializing KPIs")
	c.registeredKPIsByResourceName = make(map[string]plugins.KPI)
	// List all existing kpis and initialize them.
	var kpis v1alpha1.KPIList
	if err := c.List(ctx, &kpis); err != nil {
		return fmt.Errorf("failed to list existing kpis: %w", err)
	}
	for _, kpi := range kpis.Items {
		if kpi.Spec.SchedulingDomain != c.SchedulingDomain {
			continue
		}
		old := kpi.DeepCopy()
		err := c.handleKPIChange(ctx, &kpi)
		if err != nil {
			meta.SetStatusCondition(&kpi.Status.Conditions, metav1.Condition{
				Type:    v1alpha1.KPIConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  "ReconciliationFailed",
				Message: err.Error(),
			})
		} else {
			meta.SetStatusCondition(&kpi.Status.Conditions, metav1.Condition{
				Type:    v1alpha1.KPIConditionReady,
				Status:  metav1.ConditionTrue,
				Reason:  "ReconciliationSucceeded",
				Message: "kpi reconciled successfully",
			})
		}
		patch := client.MergeFrom(old)
		if err := c.Status().Patch(ctx, &kpi, patch); err != nil {
			log.Error(err, "failed to patch kpi status after reconciliation error", "name", kpi.Name)
		}
	}
	return nil
}

// Find a joint database connection for all given datasources.
// The returned database can be nil if no database is needed.
func (c *Controller) getJointDB(
	ctx context.Context,
	datasources []corev1.ObjectReference,
) (*db.DB, error) {
	// Check if all datasources configured share the same database secret ref.
	var databaseSecretRef *corev1.SecretReference
	for _, dsRef := range datasources {
		ds := &v1alpha1.Datasource{}
		if err := c.Get(ctx, client.ObjectKey{
			Namespace: dsRef.Namespace,
			Name:      dsRef.Name,
		}, ds); err != nil {
			if client.IgnoreNotFound(err) == nil {
				continue
			}
			return nil, err
		}
		if databaseSecretRef == nil {
			databaseSecretRef = &ds.Spec.DatabaseSecretRef
		} else if databaseSecretRef.Name != ds.Spec.DatabaseSecretRef.Name ||
			databaseSecretRef.Namespace != ds.Spec.DatabaseSecretRef.Namespace {
			return nil, errors.New("datasources have different database secret refs")
		}
	}
	// When we have datasources reading from a database, connect to it.
	var authenticatedDB *db.DB
	if databaseSecretRef != nil {
		var err error
		authenticatedDB, err = db.Connector{Client: c.Client}.
			FromSecretRef(ctx, *databaseSecretRef)
		if err != nil {
			return nil, err
		}
	}
	return authenticatedDB, nil
}

// Handle changes to a kpi resource.
func (c *Controller) handleKPIChange(ctx context.Context, obj *v1alpha1.KPI) error {
	log := ctrl.LoggerFrom(ctx)

	// Track if any datasource requires a database connection.
	var datasourcesWithDB int

	// Get all the datasources this kpi depends on, if any.
	var datasourcesReady int
	for _, dsRef := range obj.Spec.Dependencies.Datasources {
		ds := &v1alpha1.Datasource{}
		if err := c.Get(ctx, client.ObjectKey{
			Namespace: dsRef.Namespace,
			Name:      dsRef.Name,
		}, ds); err != nil {
			if client.IgnoreNotFound(err) == nil {
				continue
			}
			log.Error(err, "failed to get datasource dependency", "datasource", dsRef)
			return err
		}
		// Check if datasource is ready
		if meta.IsStatusConditionTrue(ds.Status.Conditions, v1alpha1.DatasourceConditionReady) {
			datasourcesReady++
		}

		// Check if datasource requires a database connection.
		if ds.Spec.DatabaseSecretRef.Name != "" {
			datasourcesWithDB++
		}
	}

	// Get all knowledges this kpi depends on, if any.
	var knowledgesReady int
	for _, knRef := range obj.Spec.Dependencies.Knowledges {
		kn := &v1alpha1.Knowledge{}
		if err := c.Get(ctx, client.ObjectKey{
			Namespace: knRef.Namespace,
			Name:      knRef.Name,
		}, kn); err != nil {
			if client.IgnoreNotFound(err) == nil {
				continue
			}
			log.Error(err, "failed to get knowledge dependency", "knowledge", knRef)
			return err
		}
		// Check if knowledge is ready
		if meta.IsStatusConditionTrue(kn.Status.Conditions, v1alpha1.KnowledgeConditionReady) {
			knowledgesReady++
		}
	}

	dependenciesReadyTotal := datasourcesReady + knowledgesReady
	dependenciesTotal := len(obj.Spec.Dependencies.Datasources) +
		len(obj.Spec.Dependencies.Knowledges)
	registeredKPI, registered := c.registeredKPIsByResourceName[obj.Name]

	// If all dependencies are ready but the kpi is not registered yet,
	// initialize and register it now.
	if dependenciesReadyTotal == dependenciesTotal && !registered {
		log.Info("kpi: registering new kpi", "name", obj.Name)
		var ok bool
		registeredKPI, ok = c.supportedKPIs[obj.Spec.Impl]
		if !ok {
			return fmt.Errorf("kpi %s not supported", obj.Name)
		}
		registeredKPI = &kpilogger{kpi: registeredKPI}
		// Get joint database connection for all dependencies.
		jointDB, err := c.getJointDB(ctx, obj.Spec.Dependencies.Datasources)
		if err != nil {
			return fmt.Errorf("failed to get joint database for kpi %s: %w", obj.Name, err)
		}
		if jointDB == nil && datasourcesWithDB > 0 {
			return fmt.Errorf("kpi %s has datasources requiring database but no connection available", obj.Name)
		}
		rawOpts := conf.NewRawOpts(`{}`)
		if len(obj.Spec.Opts.Raw) > 0 {
			rawOpts = conf.NewRawOptsBytes(obj.Spec.Opts.Raw)
		}
		if err := registeredKPI.Init(jointDB, c.Client, rawOpts); err != nil {
			return fmt.Errorf("failed to initialize kpi %s: %w", obj.Name, err)
		}
		if err := metrics.Registry.Register(registeredKPI); err != nil {
			return fmt.Errorf("failed to register kpi %s metrics: %w", obj.Name, err)
		}
		c.registeredKPIsByResourceName[obj.Name] = registeredKPI
	}

	// If the dependencies are not all ready but the kpi is registered,
	// unregister it.
	if dependenciesReadyTotal < dependenciesTotal && registered {
		log.Info("kpi: unregistering kpi due to unready dependencies", "name", obj.Name)
		metrics.Registry.Unregister(registeredKPI)
		delete(c.registeredKPIsByResourceName, obj.Name)
	}

	// Update the status to ready and populate the ready dependencies.
	if dependenciesReadyTotal == dependenciesTotal {
		meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.KPIConditionReady,
			Status:  metav1.ConditionTrue,
			Reason:  "AllDependenciesReady",
			Message: "all kpi dependencies are ready",
		})
	} else {
		meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
			Type:   v1alpha1.KPIConditionReady,
			Status: metav1.ConditionFalse,
			Reason: "UnreadyDependencies",
			Message: fmt.Sprintf("%d out of %d kpi dependencies are ready",
				dependenciesReadyTotal, dependenciesTotal),
		})
	}
	obj.Status.ReadyDependencies = dependenciesReadyTotal
	obj.Status.TotalDependencies = dependenciesTotal
	obj.Status.DependenciesReadyFrac = "ready"
	if dependenciesTotal > 0 {
		obj.Status.DependenciesReadyFrac = fmt.Sprintf("%d/%d",
			dependenciesReadyTotal, dependenciesTotal)
	}
	log.Info("kpi: successfully reconciled kpi", "name", obj.Name)
	return nil
}

// Handle a datasource creation, update, or delete event from watching
// datasource resources.
func (c *Controller) handleDatasourceChange(
	ctx context.Context,
	obj *v1alpha1.Datasource,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	// Find all kpis depending on this datasource and enqueue them for reconciliation.
	var kpis v1alpha1.KPIList
	if err := c.List(ctx, &kpis); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to list kpis for datasource change handling")
		return
	}
	for _, kpi := range kpis.Items {
		for _, dsRef := range kpi.Spec.Dependencies.Datasources {
			if dsRef.Name == obj.Name && dsRef.Namespace == obj.Namespace {
				queue.Add(reconcile.Request{
					NamespacedName: client.ObjectKey{
						Name: kpi.Name,
					},
				})
				break
			}
		}
	}
}

func (c *Controller) handleDatasourceCreated(
	ctx context.Context,
	evt event.CreateEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {

	ds := evt.Object.(*v1alpha1.Datasource)
	c.handleDatasourceChange(ctx, ds, queue)
}

func (c *Controller) handleDatasourceUpdated(
	ctx context.Context,
	evt event.UpdateEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {

	dsBefore := evt.ObjectNew.(*v1alpha1.Datasource)
	dsAfter := evt.ObjectOld.(*v1alpha1.Datasource)
	// Only react to changes affecting the readiness.
	dsBeforeReady := meta.IsStatusConditionTrue(dsBefore.Status.Conditions, v1alpha1.DatasourceConditionReady)
	dsAfterReady := meta.IsStatusConditionTrue(dsAfter.Status.Conditions, v1alpha1.DatasourceConditionReady)
	if dsBeforeReady == dsAfterReady {
		return
	}
	// Handle the change.
	c.handleDatasourceChange(ctx, dsAfter, queue)
}

func (c *Controller) handleDatasourceDeleted(
	ctx context.Context,
	evt event.DeleteEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {

	ds := evt.Object.(*v1alpha1.Datasource)
	c.handleDatasourceChange(ctx, ds, queue)
}

// Handle a knowledge creation, update, or delete event from watching
// knowledge resources.
func (c *Controller) handleKnowledgeChange(
	ctx context.Context,
	obj *v1alpha1.Knowledge,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	// Find all kpis depending on this knowledge and enqueue them for reconciliation.
	var kpis v1alpha1.KPIList
	if err := c.List(ctx, &kpis); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to list kpis for knowledge change handling")
		return
	}
	for _, kpi := range kpis.Items {
		for _, knRef := range kpi.Spec.Dependencies.Knowledges {
			if knRef.Name == obj.Name && knRef.Namespace == obj.Namespace {
				queue.Add(reconcile.Request{
					NamespacedName: client.ObjectKey{
						Name: kpi.Name,
					},
				})
				break
			}
		}
	}
}

func (c *Controller) handleKnowledgeCreated(
	ctx context.Context,
	evt event.CreateEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {

	kn := evt.Object.(*v1alpha1.Knowledge)
	c.handleKnowledgeChange(ctx, kn, queue)
}

func (c *Controller) handleKnowledgeUpdated(
	ctx context.Context,
	evt event.UpdateEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {

	knBefore := evt.ObjectNew.(*v1alpha1.Knowledge)
	knAfter := evt.ObjectOld.(*v1alpha1.Knowledge)
	// Only react to changes affecting the readiness.
	knBeforeReady := meta.IsStatusConditionTrue(knBefore.Status.Conditions, v1alpha1.KnowledgeConditionReady)
	knAfterReady := meta.IsStatusConditionTrue(knAfter.Status.Conditions, v1alpha1.KnowledgeConditionReady)
	if knBeforeReady == knAfterReady {
		return
	}
	// Handle the change.
	c.handleKnowledgeChange(ctx, knAfter, queue)
}

func (c *Controller) handleKnowledgeDeleted(
	ctx context.Context,
	evt event.DeleteEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {

	kn := evt.Object.(*v1alpha1.Knowledge)
	c.handleKnowledgeChange(ctx, kn, queue)
}

func (c *Controller) SetupWithManager(mgr manager.Manager, mcl *multicluster.Client) error {
	if c.supportedKPIs == nil { // This can be overridden for tests.
		c.supportedKPIs = supportedKPIs
	}
	if err := mgr.Add(manager.RunnableFunc(c.InitAllKPIs)); err != nil {
		return err
	}
	return multicluster.BuildController(mcl, mgr).
		// Watch datasource changes so that we can reconfigure kpis as needed.
		WatchesMulticluster(
			&v1alpha1.Datasource{},
			handler.Funcs{
				CreateFunc: c.handleDatasourceCreated,
				UpdateFunc: c.handleDatasourceUpdated,
				DeleteFunc: c.handleDatasourceDeleted,
			},
			predicate.NewPredicateFuncs(func(obj client.Object) bool {
				// Only react to datasources matching the scheduling domain.
				ds := obj.(*v1alpha1.Datasource)
				return ds.Spec.SchedulingDomain == c.SchedulingDomain
			}),
		).
		// Watch knowledge changes so that we can reconfigure kpis as needed.
		WatchesMulticluster(
			&v1alpha1.Knowledge{},
			handler.Funcs{
				CreateFunc: c.handleKnowledgeCreated,
				UpdateFunc: c.handleKnowledgeUpdated,
				DeleteFunc: c.handleKnowledgeDeleted,
			},
			predicate.NewPredicateFuncs(func(obj client.Object) bool {
				// Only react to knowledges matching the scheduling domain.
				kn := obj.(*v1alpha1.Knowledge)
				return kn.Spec.SchedulingDomain == c.SchedulingDomain
			}),
		).
		Named("cortex-kpis").
		For(
			&v1alpha1.KPI{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				// Only react to datasources matching the scheduling domain.
				ds := obj.(*v1alpha1.KPI)
				return ds.Spec.SchedulingDomain == c.SchedulingDomain
			})),
		).
		Complete(c)
}
