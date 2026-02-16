// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	"github.com/cobaltcore-dev/cortex/pkg/sso"
	"github.com/sapcc/go-bits/jobloop"
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

type OpenStackDatasourceReconcilerConfig struct {
	// The controller will only touch resources with this scheduling domain.
	SchedulingDomain v1alpha1.SchedulingDomain `json:"schedulingDomain"`
	// Secret ref to keystone credentials stored in a k8s secret.
	KeystoneSecretRef corev1.SecretReference `json:"keystoneSecretRef"`
	// Secret ref to SSO credentials stored in a k8s secret, if applicable.
	SSOSecretRef *corev1.SecretReference `json:"ssoSecretRef"`
}

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
	Conf OpenStackDatasourceReconcilerConfig
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
	if datasource.Status.NextSyncTime.After(time.Now()) && datasource.Status.NumberOfObjects != 0 {
		log.Info("skipping datasource sync, not yet time", "name", datasource.Name)
		return ctrl.Result{RequeueAfter: time.Until(datasource.Status.NextSyncTime.Time)}, nil
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

	// Authenticate with keystone.
	authenticatedKeystone, err := keystone.Connector{Client: r.Client, HTTPClient: authenticatedHTTP}.
		FromSecretRef(ctx, datasource.Spec.OpenStack.SecretRef)
	if err != nil {
		log.Error(err, "failed to authenticate with keystone", "secretRef", datasource.Spec.OpenStack.SecretRef)
		old := datasource.DeepCopy()
		meta.SetStatusCondition(&datasource.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DatasourceConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "KeystoneAuthenticationFailed",
			Message: "failed to authenticate with keystone: " + err.Error(),
		})
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, datasource, patch); err != nil {
			log.Error(err, "failed to patch datasource status", "name", datasource.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	syncer, err := getSupportedSyncer(
		*datasource,
		authenticatedDB,
		authenticatedKeystone,
		r.Monitor,
	)
	if err != nil {
		log.Info("skipping datasource, unsupported openstack datasource type", "type", datasource.Spec.OpenStack.Type)
		old := datasource.DeepCopy()
		meta.SetStatusCondition(&datasource.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DatasourceConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "UnsupportedOpenStackDatasourceType",
			Message: "unsupported openstack datasource type: " + string(datasource.Spec.OpenStack.Type),
		})
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, datasource, patch); err != nil {
			log.Error(err, "failed to patch datasource status", "name", datasource.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Initialize the syncer before syncing.
	if err := syncer.Init(ctx); err != nil {
		log.Error(err, "failed to init openstack datasource", "name", datasource.Name)
		old := datasource.DeepCopy()
		meta.SetStatusCondition(&datasource.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DatasourceConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "OpenStackDatasourceInitFailed",
			Message: "failed to init openstack datasource: " + err.Error(),
		})
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, datasource, patch); err != nil {
			log.Error(err, "failed to patch datasource status", "name", datasource.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	nResults, err := syncer.Sync(ctx)
	if errors.Is(err, v1alpha1.ErrWaitingForDependencyDatasource) {
		log.Info("datasource sync waiting for dependency datasource", "name", datasource.Name)
		old := datasource.DeepCopy()
		meta.SetStatusCondition(&datasource.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DatasourceConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "WaitingForDependencyDatasource",
			Message: "waiting for dependency datasource",
		})
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, datasource, patch); err != nil {
			log.Error(err, "failed to patch datasource status", "name", datasource.Name)
			return ctrl.Result{}, err
		}
		// Requeue after a short delay to check again.
		return ctrl.Result{RequeueAfter: jobloop.DefaultJitter(time.Minute)}, nil
	}
	// Other error
	if err != nil {
		log.Error(err, "failed to sync openstack datasource", "name", datasource.Name)
		old := datasource.DeepCopy()
		meta.SetStatusCondition(&datasource.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DatasourceConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "OpenStackDatasourceSyncFailed",
			Message: "failed to sync openstack datasource: " + err.Error(),
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
		Reason:  "OpenStackDatasourceSynced",
		Message: "openstack datasource synced successfully",
	})
	datasource.Status.LastSynced = metav1.NewTime(time.Now())
	nextTime := time.Now().Add(datasource.Spec.OpenStack.SyncInterval.Duration)
	datasource.Status.NextSyncTime = metav1.NewTime(nextTime)
	datasource.Status.NumberOfObjects = nResults
	patch := client.MergeFrom(old)
	if err := r.Status().Patch(ctx, datasource, patch); err != nil {
		log.Error(err, "failed to patch datasource status", "name", datasource.Name)
		return ctrl.Result{}, err
	}

	// Calculate the next sync time based on the configured sync interval.
	return ctrl.Result{RequeueAfter: time.Until(nextTime)}, nil
}

func (r *OpenStackDatasourceReconciler) SetupWithManager(mgr manager.Manager, mcl *multicluster.Client) error {
	return multicluster.BuildController(mcl, mgr).
		Named("cortex-openstack-datasource").
		For(
			&v1alpha1.Datasource{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				// Only react to datasources matching the operator.
				ds := obj.(*v1alpha1.Datasource)
				if ds.Spec.SchedulingDomain != r.Conf.SchedulingDomain {
					return false
				}
				// Only react to openstack datasources.
				return ds.Spec.Type == v1alpha1.DatasourceTypeOpenStack
			})),
		).
		Complete(r)
}
