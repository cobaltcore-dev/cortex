// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"context"
	"net/http"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
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

type PrometheusDatasourceReconcilerConfig struct {
	// The controller will only touch resources with this scheduling domain.
	SchedulingDomain v1alpha1.SchedulingDomain `json:"schedulingDomain"`
	// Secret ref to keystone credentials stored in a k8s secret.
	KeystoneSecretRef corev1.SecretReference `json:"keystoneSecretRef"`
	// Secret ref to SSO credentials stored in a k8s secret, if applicable.
	SSOSecretRef *corev1.SecretReference `json:"ssoSecretRef"`
}

type PrometheusDatasourceReconciler struct {
	// Client for the kubernetes API.
	client.Client
	// Kubernetes scheme to use for the deschedulings.
	Scheme *runtime.Scheme
	// Config for the reconciler.
	Conf PrometheusDatasourceReconcilerConfig
	// Monitor for tracking the datasource syncs.
	Monitor datasources.Monitor
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *PrometheusDatasourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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
	if datasource.Status.NextSyncTime.After(time.Now()) && datasource.Status.NumberOfObjects != 0 {
		log.Info("skipping datasource sync, not yet time", "name", datasource.Name)
		return ctrl.Result{RequeueAfter: time.Until(datasource.Status.NextSyncTime.Time)}, nil
	}

	newSyncerFunc, ok := supportedMetricSyncers[datasource.Spec.Prometheus.Type]
	if !ok {
		log.Info("skipping datasource, unsupported metric type", "metricType", datasource.Spec.Prometheus.Type)
		old := datasource.DeepCopy()
		meta.SetStatusCondition(&datasource.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DatasourceConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "UnsupportedPrometheusMetricType",
			Message: "unsupported metric type: " + datasource.Spec.Prometheus.Type,
		})
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, datasource, patch); err != nil {
			log.Error(err, "failed to patch datasource status", "name", datasource.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Authenticate with the database based on the secret provided in the datasource.
	authenticatedDB, err := db.Connector{Client: r.Client}.
		FromSecretRef(ctx, datasource.Spec.DatabaseSecretRef)
	if err != nil {
		log.Error(err, "failed to authenticate with database", "secretRef", datasource.Spec.DatabaseSecretRef)
		old := datasource.DeepCopy()
		meta.SetStatusCondition(&datasource.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DatasourceConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "DatabaseAuthenticationFailed",
			Message: "failed to authenticate with database: " + err.Error(),
		})
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, datasource, patch); err != nil {
			log.Error(err, "failed to patch datasource status", "name", datasource.Name)
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
			old := datasource.DeepCopy()
			meta.SetStatusCondition(&datasource.Status.Conditions, metav1.Condition{
				Type:    v1alpha1.DatasourceConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  "SSOAuthenticationFailed",
				Message: "failed to authenticate with SSO: " + err.Error(),
			})
			patch := client.MergeFrom(old)
			if err := r.Status().Patch(ctx, datasource, patch); err != nil {
				log.Error(err, "failed to patch datasource status", "name", datasource.Name)
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
		old := datasource.DeepCopy()
		meta.SetStatusCondition(&datasource.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DatasourceConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "MissingPrometheusURL",
			Message: "missing 'url' in prometheus secret",
		})
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, datasource, patch); err != nil {
			log.Error(err, "failed to patch datasource status", "name", datasource.Name)
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
		old := datasource.DeepCopy()
		meta.SetStatusCondition(&datasource.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DatasourceConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "PrometheusDatasourceSyncFailed",
			Message: "failed to sync prometheus datasource: " + err.Error(),
		})
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, datasource, patch); err != nil {
			log.Error(err, "failed to patch datasource status", "name", datasource.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	// Update the datasource status to reflect successful sync.
	old := datasource.DeepCopy()
	meta.SetStatusCondition(&datasource.Status.Conditions, metav1.Condition{
		Type:    v1alpha1.DatasourceConditionReady,
		Status:  metav1.ConditionTrue,
		Reason:  "PrometheusDatasourceSynced",
		Message: "prometheus datasource synced successfully",
	})
	datasource.Status.LastSynced = metav1.NewTime(time.Now())
	datasource.Status.NextSyncTime = metav1.NewTime(nextSync)
	datasource.Status.NumberOfObjects = nResults
	patch := client.MergeFrom(old)
	if err := r.Status().Patch(ctx, datasource, patch); err != nil {
		log.Error(err, "failed to patch datasource status", "name", datasource.Name)
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
				if ds.Spec.SchedulingDomain != r.Conf.SchedulingDomain {
					return false
				}
				// Only react to prometheus datasources.
				return ds.Spec.Type == v1alpha1.DatasourceTypePrometheus
			})),
		).
		Complete(r)
}
