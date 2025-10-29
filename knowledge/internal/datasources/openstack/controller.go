// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/conf"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/datasources"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/datasources/openstack/cinder"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/datasources/openstack/identity"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/datasources/openstack/limes"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/datasources/openstack/manila"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/datasources/openstack/placement"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/lib/keystone"
	"github.com/cobaltcore-dev/cortex/lib/sso"
	"github.com/sapcc/go-bits/jobloop"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type Syncer interface {
	// Init the syncer, e.g. create the database tables.
	Init(context.Context) error
	// Sync the datasource and return the number of objects + an error if any.
	Sync(context.Context) (int64, error)
}

type OpenStackDatasourceReconciler struct {
	// Client for the kubernetes API.
	client.Client
	// Kubernetes scheme to use for the deschedulings.
	Scheme *runtime.Scheme
	// Datasources monitor.
	Monitor datasources.Monitor
	// Config for the reconciler.
	Conf conf.Config
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *OpenStackDatasourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startedAt := time.Now() // So we can measure sync duration.
	log := logf.FromContext(ctx)
	datasource := &v1alpha1.Datasource{}
	if err := r.Get(ctx, req.NamespacedName, datasource); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Sanity checks.
	if datasource.Spec.Type != v1alpha1.DatasourceTypeOpenStack {
		log.Info("skipping datasource, not an openstack datasource", "name", datasource.Name)
		return ctrl.Result{}, nil
	}
	if datasource.Status.NextSyncTime.Time.After(time.Now()) {
		log.Info("skipping datasource sync, not yet time", "name", datasource.Name)
		return ctrl.Result{RequeueAfter: time.Until(datasource.Status.NextSyncTime.Time)}, nil
	}

	// Authenticate with the database based on the secret provided in the datasource.
	authenticatedDB, err := db.Connector{Client: r.Client}.
		FromSecretRef(ctx, datasource.Spec.DatabaseSecretRef)
	if err != nil {
		log.Error(err, "failed to authenticate with database", "secretRef", datasource.Spec.DatabaseSecretRef)
		meta.SetStatusCondition(&datasource.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DatasourceConditionError,
			Status:  metav1.ConditionTrue,
			Reason:  "DatabaseAuthenticationFailed",
			Message: "failed to authenticate with database: " + err.Error(),
		})
		if err := r.Status().Update(ctx, datasource); err != nil {
			log.Error(err, "failed to update datasource status", "name", datasource.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}
	defer authenticatedDB.Close()

	// Authenticate with the datasource host if SSO is configured.
	var authenticatedHTTP = http.DefaultClient
	if datasource.Spec.SSOSecretRef != nil {
		authenticatedHTTP, err = sso.Connector{Client: r.Client}.
			FromSecretRef(ctx, *datasource.Spec.SSOSecretRef)
		if err != nil {
			log.Error(err, "failed to authenticate with SSO", "secretRef", datasource.Spec.SSOSecretRef)
			meta.SetStatusCondition(&datasource.Status.Conditions, metav1.Condition{
				Type:    v1alpha1.DatasourceConditionError,
				Status:  metav1.ConditionTrue,
				Reason:  "SSOAuthenticationFailed",
				Message: "failed to authenticate with SSO: " + err.Error(),
			})
			if err := r.Status().Update(ctx, datasource); err != nil {
				log.Error(err, "failed to update datasource status", "name", datasource.Name)
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}
	}

	// Authenticate with keystone.
	authenticatedKeystone, err := keystone.Connector{Client: r.Client, HTTPClient: authenticatedHTTP}.
		FromSecretRef(ctx, datasource.Spec.OpenStack.SecretRef)
	if err != nil {
		log.Error(err, "failed to authenticate with keystone", "secretRef", datasource.Spec.OpenStack.SecretRef)
		meta.SetStatusCondition(&datasource.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DatasourceConditionError,
			Status:  metav1.ConditionTrue,
			Reason:  "KeystoneAuthenticationFailed",
			Message: "failed to authenticate with keystone: " + err.Error(),
		})
		if err := r.Status().Update(ctx, datasource); err != nil {
			log.Error(err, "failed to update datasource status", "name", datasource.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	var syncer Syncer
	switch datasource.Spec.OpenStack.Type {
	case v1alpha1.OpenStackDatasourceTypeNova:
		syncer = &nova.NovaSyncer{
			DB:   *authenticatedDB,
			Mon:  r.Monitor,
			Conf: datasource.Spec.OpenStack.Nova,
			API:  nova.NewNovaAPI(r.Monitor, authenticatedKeystone, datasource.Spec.OpenStack.Nova),
		}
	case v1alpha1.OpenStackDatasourceTypeManila:
		syncer = &manila.ManilaSyncer{
			DB:   *authenticatedDB,
			Mon:  r.Monitor,
			Conf: datasource.Spec.OpenStack.Manila,
			API:  manila.NewManilaAPI(r.Monitor, authenticatedKeystone, datasource.Spec.OpenStack.Manila),
		}
	case v1alpha1.OpenStackDatasourceTypePlacement:
		syncer = &placement.PlacementSyncer{
			DB:   *authenticatedDB,
			Mon:  r.Monitor,
			Conf: datasource.Spec.OpenStack.Placement,
			API:  placement.NewPlacementAPI(r.Monitor, authenticatedKeystone, datasource.Spec.OpenStack.Placement),
		}
	case v1alpha1.OpenStackDatasourceTypeIdentity:
		syncer = &identity.IdentitySyncer{
			DB:   *authenticatedDB,
			Mon:  r.Monitor,
			Conf: datasource.Spec.OpenStack.Identity,
			API:  identity.NewIdentityAPI(r.Monitor, authenticatedKeystone, datasource.Spec.OpenStack.Identity),
		}
	case v1alpha1.OpenStackDatasourceTypeLimes:
		syncer = &limes.LimesSyncer{
			DB:   *authenticatedDB,
			Mon:  r.Monitor,
			Conf: datasource.Spec.OpenStack.Limes,
			API:  limes.NewLimesAPI(r.Monitor, authenticatedKeystone, datasource.Spec.OpenStack.Limes),
		}
	case v1alpha1.OpenStackDatasourceTypeCinder:
		syncer = &cinder.CinderSyncer{
			DB:   *authenticatedDB,
			Mon:  r.Monitor,
			Conf: datasource.Spec.OpenStack.Cinder,
			API:  cinder.NewCinderAPI(r.Monitor, authenticatedKeystone, datasource.Spec.OpenStack.Cinder),
		}
	default:
		log.Info("skipping datasource, unsupported openstack datasource type", "type", datasource.Spec.OpenStack.Type)
		meta.SetStatusCondition(&datasource.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DatasourceConditionError,
			Status:  metav1.ConditionTrue,
			Reason:  "UnsupportedOpenStackDatasourceType",
			Message: "unsupported openstack datasource type: " + string(datasource.Spec.OpenStack.Type),
		})
		if err := r.Status().Update(ctx, datasource); err != nil {
			log.Error(err, "failed to update datasource status", "name", datasource.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Initialize the syncer before syncing.
	if err := syncer.Init(ctx); err != nil {
		log.Error(err, "failed to init openstack datasource", "name", datasource.Name)
		meta.SetStatusCondition(&datasource.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DatasourceConditionError,
			Status:  metav1.ConditionTrue,
			Reason:  "OpenStackDatasourceInitFailed",
			Message: "failed to init openstack datasource: " + err.Error(),
		})
		if err := r.Status().Update(ctx, datasource); err != nil {
			log.Error(err, "failed to update datasource status", "name", datasource.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	nResults, err := syncer.Sync(ctx)
	if errors.Is(err, v1alpha1.ErrWaitingForDependencyDatasource) {
		log.Info("datasource sync waiting for dependency datasource", "name", datasource.Name)
		meta.SetStatusCondition(&datasource.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DatasourceConditionWaiting,
			Status:  metav1.ConditionTrue,
			Reason:  "WaitingForDependencyDatasource",
			Message: "waiting for dependency datasource",
		})
		if err := r.Status().Update(ctx, datasource); err != nil {
			log.Error(err, "failed to update datasource status", "name", datasource.Name)
			return ctrl.Result{}, err
		}
		// Requeue after a short delay to check again.
		return ctrl.Result{RequeueAfter: jobloop.DefaultJitter(time.Minute)}, nil
	}
	// Other error
	if err != nil {
		log.Error(err, "failed to sync openstack datasource", "name", datasource.Name)
		meta.SetStatusCondition(&datasource.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DatasourceConditionError,
			Status:  metav1.ConditionTrue,
			Reason:  "OpenStackDatasourceSyncFailed",
			Message: "failed to sync openstack datasource: " + err.Error(),
		})
		if err := r.Status().Update(ctx, datasource); err != nil {
			log.Error(err, "failed to update datasource status", "name", datasource.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	// Update the datasource status to reflect successful sync.
	meta.RemoveStatusCondition(&datasource.Status.Conditions, v1alpha1.DatasourceConditionError)
	meta.RemoveStatusCondition(&datasource.Status.Conditions, v1alpha1.DatasourceConditionWaiting)
	datasource.Status.LastSynced = metav1.NewTime(time.Now())
	nextTime := time.Now().Add(datasource.Spec.OpenStack.SyncInterval.Duration)
	datasource.Status.NextSyncTime = metav1.NewTime(nextTime)
	datasource.Status.NumberOfObjects = nResults
	datasource.Status.Took = metav1.Duration{Duration: time.Since(startedAt)}
	if err := r.Status().Update(ctx, datasource); err != nil {
		log.Error(err, "failed to update datasource status", "name", datasource.Name)
		return ctrl.Result{}, err
	}

	// Calculate the next sync time based on the configured sync interval.
	return ctrl.Result{RequeueAfter: time.Until(nextTime)}, nil
}

func (r *OpenStackDatasourceReconciler) SetupWithManager(mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("cortex-openstack-datasource").
		For(
			&v1alpha1.Datasource{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				// Only react to datasources matching the operator.
				ds := obj.(*v1alpha1.Datasource)
				if ds.Spec.Operator != r.Conf.Operator {
					return false
				}
				// Only react to openstack datasources.
				return ds.Spec.Type == v1alpha1.DatasourceTypeOpenStack
			})),
		).
		Complete(r)
}
