// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"context"
	"net/http"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	"github.com/cobaltcore-dev/cortex/pkg/sso"
	corev1 "k8s.io/api/core/v1"
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

type PrometheusDatasourceReconciler struct {
	// Client for the kubernetes API.
	client.Client
	// Kubernetes scheme to use for the deschedulings.
	Scheme *runtime.Scheme
	// Config for the reconciler.
	Conf conf.Config
	// Monitor for tracking the datasource syncs.
	Monitor datasources.Monitor
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *PrometheusDatasourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startedAt := time.Now() // So we can measure sync duration.
	log := logf.FromContext(ctx)
	datasource := &v1alpha1.Datasource{}
	if err := r.Get(ctx, req.NamespacedName, datasource); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Sanity checks.
	if datasource.Spec.Type != v1alpha1.DatasourceTypePrometheus {
		log.Info("skipping datasource, not a prometheus datasource", "name", datasource.Name)
		return ctrl.Result{}, nil
	}
	if datasource.Status.NextSyncTime.After(time.Now()) {
		log.Info("skipping datasource sync, not yet time", "name", datasource.Name)
		return ctrl.Result{RequeueAfter: time.Until(datasource.Status.NextSyncTime.Time)}, nil
	}

	newSyncerFunc, ok := supportedMetricSyncers[datasource.Spec.Prometheus.Type]
	if !ok {
		log.Info("skipping datasource, unsupported metric type", "metricType", datasource.Spec.Prometheus.Type)
		meta.SetStatusCondition(&datasource.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DatasourceConditionError,
			Status:  metav1.ConditionTrue,
			Reason:  "UnsupportedPrometheusMetricType",
			Message: "unsupported metric type: " + datasource.Spec.Prometheus.Type,
		})
		if err := r.Status().Update(ctx, datasource); err != nil {
			log.Error(err, "failed to update datasource status", "name", datasource.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
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

	// Get the prometheus URL from the secret ref.
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: datasource.Spec.Prometheus.SecretRef.Namespace,
		Name:      datasource.Spec.Prometheus.SecretRef.Name,
	}, secret); err != nil {
		return ctrl.Result{}, err
	}
	prometheusURL, ok := secret.Data["url"]
	if !ok {
		log.Error(err, "missing 'url' in prometheus secret", "secretRef", datasource.Spec.Prometheus.SecretRef)
		meta.SetStatusCondition(&datasource.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DatasourceConditionError,
			Status:  metav1.ConditionTrue,
			Reason:  "MissingPrometheusURL",
			Message: "missing 'url' in prometheus secret",
		})
		if err := r.Status().Update(ctx, datasource); err != nil {
			log.Error(err, "failed to update datasource status", "name", datasource.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	syncer := newSyncerFunc(
		*datasource,
		authenticatedDB,
		authenticatedHTTP,
		string(prometheusURL),
		r.Monitor,
	)
	nResults, nextSync, err := syncer.Sync(ctx)
	if err != nil {
		log.Error(err, "failed to sync prometheus datasource", "name", datasource.Name)
		meta.SetStatusCondition(&datasource.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DatasourceConditionError,
			Status:  metav1.ConditionTrue,
			Reason:  "PrometheusDatasourceSyncFailed",
			Message: "failed to sync prometheus datasource: " + err.Error(),
		})
		if err := r.Status().Update(ctx, datasource); err != nil {
			log.Error(err, "failed to update datasource status", "name", datasource.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	// Update the datasource status to reflect successful sync.
	meta.RemoveStatusCondition(&datasource.Status.Conditions, v1alpha1.DatasourceConditionError)
	datasource.Status.LastSynced = metav1.NewTime(time.Now())
	datasource.Status.NextSyncTime = metav1.NewTime(nextSync)
	datasource.Status.NumberOfObjects = nResults
	datasource.Status.Took = metav1.Duration{Duration: time.Since(startedAt)}
	if err := r.Status().Update(ctx, datasource); err != nil {
		log.Error(err, "failed to update datasource status", "name", datasource.Name)
		return ctrl.Result{}, err
	}

	// Calculate the next sync time based on the configured sync interval.
	return ctrl.Result{RequeueAfter: time.Until(nextSync)}, nil
}

func (r *PrometheusDatasourceReconciler) SetupWithManager(mgr manager.Manager, mcl *multicluster.Client) error {
	return multicluster.BuildController(mcl, mgr).
		Named("cortex-prometheus-datasource").
		For(
			&v1alpha1.Datasource{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				// Only react to datasources matching the operator.
				ds := obj.(*v1alpha1.Datasource)
				if ds.Spec.Operator != r.Conf.Operator {
					return false
				}
				// Only react to prometheus datasources.
				return ds.Spec.Type == v1alpha1.DatasourceTypePrometheus
			})),
		).
		Complete(r)
}
