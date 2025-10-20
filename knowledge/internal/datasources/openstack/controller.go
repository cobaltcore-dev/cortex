// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"net/http"
	"time"

	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/lib/sso"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type OpenStackDatasourceReconciler struct {
	// Client for the kubernetes API.
	client.Client
	// Kubernetes scheme to use for the deschedulings.
	Scheme *runtime.Scheme
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *OpenStackDatasourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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
	if datasource.Spec.OpenStack == nil {
		log.Info("skipping datasource, openstack datasource spec empty", "name", datasource.Name)
		return ctrl.Result{}, nil
	}
	if datasource.Status.NextSyncTime.Time.After(time.Now()) {
		log.Info("skipping datasource sync, not yet time", "name", datasource.Name)
		return ctrl.Result{RequeueAfter: time.Until(datasource.Status.NextSyncTime.Time)}, nil
	}

	// Authenticate with the database based on the secret provided in the datasource.
	authenticatedDB, err := db.Authenticator{Client: r.Client}.
		FromSecretRef(ctx, datasource.Spec.DatabaseSecretRef)
	if err != nil {
		log.Error(err, "failed to authenticate with database", "secretRef", datasource.Spec.DatabaseSecretRef)
		datasource.Status.Error = "failed to authenticate with database: " + err.Error()
		if err := r.Status().Update(ctx, datasource); err != nil {
			log.Error(err, "failed to update datasource status", "name", datasource.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	// Authenticate with the datasource host if SSO is configured.
	var authenticatedHTTP = http.DefaultClient
	if datasource.Spec.SSOSecretRef != nil {
		authenticatedHTTP, err = sso.Authenticator{Client: r.Client}.
			FromSecretRef(ctx, *datasource.Spec.SSOSecretRef)
		if err != nil {
			log.Error(err, "failed to authenticate with SSO", "secretRef", datasource.Spec.SSOSecretRef)
			datasource.Status.Error = "failed to authenticate with SSO: " + err.Error()
			if err := r.Status().Update(ctx, datasource); err != nil {
				log.Error(err, "failed to update datasource status", "name", datasource.Name)
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}
	}

	var syncer Syncer
	switch datasource.Spec.OpenStack.Type {
	case v1alpha1.OpenStackDatasourceTypeNova:
		syncer = &nova.NovaSyncer{
			DB:         db,
			Mon:        monitor,
			Conf:       config.Nova,
			API:        nova.NewNovaAPI(monitor, keystoneAPI, config.Nova),
			MqttClient: mqttClient,
		}
	}
	// TODO
	success, err := syncer.Sync(ctx)
	if err != nil {
		log.Error(err, "failed to sync openstack datasource", "name", datasource.Name)
		datasource.Status.Error = "failed to sync openstack datasource: " + err.Error()
		if err := r.Status().Update(ctx, datasource); err != nil {
			log.Error(err, "failed to update datasource status", "name", datasource.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	// Update the datasource status to reflect successful sync.
	datasource.Status = *success
	if err := r.Status().Update(ctx, datasource); err != nil {
		log.Error(err, "failed to update datasource status", "name", datasource.Name)
		return ctrl.Result{}, err
	}

	// Calculate the next sync time based on the configured sync interval.
	return ctrl.Result{RequeueAfter: time.Until(success.NextSyncTime.Time)}, nil
}

func (r *OpenStackDatasourceReconciler) SetupWithManager(mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("cortex-openstack-datasource").
		For(
			&v1alpha1.Datasource{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				// Only react to openstack datasources.
				return obj.(*v1alpha1.Datasource).Spec.Type == v1alpha1.DatasourceTypeOpenStack
			})),
		).
		Complete(r)
}
