// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"context"
	"net/http"
	"time"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/prometheus"
	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/lib/sso"
	corev1 "k8s.io/api/core/v1"
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
	if datasource.Status.NextSyncTime.Time.After(time.Now()) {
		log.Info("skipping datasource sync, not yet time", "name", datasource.Name)
		return ctrl.Result{RequeueAfter: time.Until(datasource.Status.NextSyncTime.Time)}, nil
	}

	newSyncerFunc, ok := map[string]func(
		ds v1alpha1.Datasource,
		authenticatedDB *db.DB,
		authenticatedHTTP *http.Client,
		prometheusURL string,
	) typedSyncer{
		"vrops_host_metric":                     newTypedSyncer[prometheus.VROpsHostMetric],
		"vrops_vm_metric":                       newTypedSyncer[prometheus.VROpsVMMetric],
		"node_exporter_metric":                  newTypedSyncer[prometheus.NodeExporterMetric],
		"netapp_aggregate_labels_metric":        newTypedSyncer[prometheus.NetAppAggregateLabelsMetric],
		"netapp_node_metric":                    newTypedSyncer[prometheus.NetAppNodeMetric],
		"netapp_volume_aggregate_labels_metric": newTypedSyncer[prometheus.NetAppVolumeAggrLabelsMetric],
		"kvm_libvirt_domain_metric":             newTypedSyncer[prometheus.KVMDomainMetric],
	}[datasource.Spec.Prometheus.Type]
	if !ok {
		log.Info("skipping datasource, unsupported metric type", "metricType", datasource.Spec.Prometheus.Type)
		datasource.Status.Error = "unsupported metric type: " + datasource.Spec.Prometheus.Type
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
		authenticatedHTTP, err = sso.Connector{Client: r.Client}.
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
		datasource.Status.Error = "missing 'url' in prometheus secret"
		if err := r.Status().Update(ctx, datasource); err != nil {
			log.Error(err, "failed to update datasource status", "name", datasource.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	syncer := newSyncerFunc(*datasource, authenticatedDB, authenticatedHTTP, string(prometheusURL))
	nResults, nextSync, err := syncer.Sync(ctx)
	if err != nil {
		log.Error(err, "failed to sync prometheus datasource", "name", datasource.Name)
		datasource.Status.Error = "failed to sync prometheus datasource: " + err.Error()
		if err := r.Status().Update(ctx, datasource); err != nil {
			log.Error(err, "failed to update datasource status", "name", datasource.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	// Update the datasource status to reflect successful sync.
	datasource.Status.Error = ""
	datasource.Status.LastSynced = metav1.NewTime(time.Now())
	datasource.Status.NextSyncTime = metav1.NewTime(nextSync)
	datasource.Status.NumberOfObjects = nResults
	datasource.Status.LastSyncDurationSeconds = int64(time.Since(startedAt).Seconds())
	if err := r.Status().Update(ctx, datasource); err != nil {
		log.Error(err, "failed to update datasource status", "name", datasource.Name)
		return ctrl.Result{}, err
	}

	// Calculate the next sync time based on the configured sync interval.
	return ctrl.Result{RequeueAfter: time.Until(nextSync)}, nil
}

func (r *PrometheusDatasourceReconciler) SetupWithManager(mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
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
