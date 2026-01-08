// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"context"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
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

type KnowledgeReconciler struct {
	// Client for the kubernetes API.
	client.Client
	// Kubernetes scheme to use for the deschedulings.
	Scheme *runtime.Scheme
	// Monitor to use for tracking the reconciler.
	Monitor Monitor
	// Config for the reconciler.
	Conf conf.Config
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *KnowledgeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startedAt := time.Now() // So we can measure sync duration.
	log := logf.FromContext(ctx)
	knowledge := &v1alpha1.Knowledge{}
	if err := r.Get(ctx, req.NamespacedName, knowledge); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Sanity checks.
	lastExtracted := knowledge.Status.LastExtracted.Time
	recency := knowledge.Spec.Recency.Duration
	if lastExtracted.Add(recency).After(time.Now()) {
		log.Info("skipping knowledge extraction, not yet time", "name", knowledge.Name)
		return ctrl.Result{RequeueAfter: time.Until(lastExtracted.Add(recency))}, nil
	}

	extractor, ok := supportedExtractors[knowledge.Spec.Extractor.Name]
	if !ok {
		log.Info("skipping knowledge extraction, unsupported extractor", "name", knowledge.Spec.Extractor.Name)
		old := knowledge.DeepCopy()
		meta.SetStatusCondition(&knowledge.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.KnowledgeConditionError,
			Status:  metav1.ConditionTrue,
			Reason:  "UnsupportedExtractor",
			Message: "unsupported extractor name: " + knowledge.Spec.Extractor.Name,
		})
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, knowledge, patch); err != nil {
			log.Error(err, "failed to patch knowledge status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Check if all datasources configured share the same database secret ref.
	var databaseSecretRef *corev1.SecretReference
	for _, dsRef := range knowledge.Spec.Dependencies.Datasources {
		ds := &v1alpha1.Datasource{}
		if err := r.Get(ctx, client.ObjectKey{
			Namespace: req.Namespace,
			Name:      dsRef.Name,
		}, ds); err != nil {
			log.Error(err, "failed to get datasource", "name", dsRef.Name)
			old := knowledge.DeepCopy()
			meta.SetStatusCondition(&knowledge.Status.Conditions, metav1.Condition{
				Type:    v1alpha1.KnowledgeConditionError,
				Status:  metav1.ConditionTrue,
				Reason:  "DatasourceFetchFailed",
				Message: "failed to get datasource: " + err.Error(),
			})
			patch := client.MergeFrom(old)
			if err := r.Status().Patch(ctx, knowledge, patch); err != nil {
				log.Error(err, "failed to patch knowledge status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}
		if databaseSecretRef == nil {
			databaseSecretRef = &ds.Spec.DatabaseSecretRef
		} else if databaseSecretRef.Name != ds.Spec.DatabaseSecretRef.Name ||
			databaseSecretRef.Namespace != ds.Spec.DatabaseSecretRef.Namespace {
			log.Error(nil, "datasources have differing database secret refs")
			old := knowledge.DeepCopy()
			meta.SetStatusCondition(&knowledge.Status.Conditions, metav1.Condition{
				Type:    v1alpha1.KnowledgeConditionError,
				Status:  metav1.ConditionTrue,
				Reason:  "InconsistentDatabaseSecretRefs",
				Message: "datasources have differing database secret refs",
			})
			patch := client.MergeFrom(old)
			if err := r.Status().Patch(ctx, knowledge, patch); err != nil {
				log.Error(err, "failed to patch knowledge status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
	}
	// When we have datasources reading from a database, connect to it.
	var authenticatedDatasourceDB *db.DB
	if databaseSecretRef != nil {
		var err error
		authenticatedDatasourceDB, err = db.Connector{Client: r.Client}.
			FromSecretRef(ctx, *databaseSecretRef)
		if err != nil {
			log.Error(err, "failed to authenticate with database", "secretRef", *databaseSecretRef)
			old := knowledge.DeepCopy()
			meta.SetStatusCondition(&knowledge.Status.Conditions, metav1.Condition{
				Type:    v1alpha1.KnowledgeConditionError,
				Status:  metav1.ConditionTrue,
				Reason:  "DatabaseAuthenticationFailed",
				Message: "failed to authenticate with database: " + err.Error(),
			})
			patch := client.MergeFrom(old)
			if err := r.Status().Patch(ctx, knowledge, patch); err != nil {
				log.Error(err, "failed to patch knowledge status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}
	}

	// Initialize and run the extractor.
	wrapped := monitorFeatureExtractor(knowledge.Spec.Extractor.Name, extractor, r.Monitor)
	if err := wrapped.Init(authenticatedDatasourceDB, r.Client, knowledge.Spec); err != nil {
		log.Error(err, "failed to initialize feature extractor", "name", knowledge.Spec)
		old := knowledge.DeepCopy()
		meta.SetStatusCondition(&knowledge.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.KnowledgeConditionError,
			Status:  metav1.ConditionTrue,
			Reason:  "FeatureExtractorInitializationFailed",
			Message: "failed to initialize feature extractor: " + err.Error(),
		})
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, knowledge, patch); err != nil {
			log.Error(err, "failed to patch knowledge status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	features, err := extractor.Extract()
	if err != nil {
		log.Error(err, "failed to extract features", "name", knowledge.Spec.Extractor.Name)
		old := knowledge.DeepCopy()
		meta.SetStatusCondition(&knowledge.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.KnowledgeConditionError,
			Status:  metav1.ConditionTrue,
			Reason:  "FeatureExtractionFailed",
			Message: "failed to extract features: " + err.Error(),
		})
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, knowledge, patch); err != nil {
			log.Error(err, "failed to patch knowledge status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	// Update the knowledge status.
	old := knowledge.DeepCopy()
	meta.RemoveStatusCondition(&knowledge.Status.Conditions, v1alpha1.KnowledgeConditionError)
	raw, err := v1alpha1.BoxFeatureList(features)
	if err != nil {
		log.Error(err, "failed to marshal extracted features", "name", knowledge.Spec.Extractor.Name)
		meta.SetStatusCondition(&knowledge.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.KnowledgeConditionError,
			Status:  metav1.ConditionTrue,
			Reason:  "FeatureMarshalingFailed",
			Message: "failed to marshal extracted features: " + err.Error(),
		})
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, knowledge, patch); err != nil {
			log.Error(err, "failed to patch knowledge status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}
	knowledge.Status.Raw = raw
	knowledge.Status.LastExtracted = metav1.NewTime(time.Now())
	knowledge.Status.RawLength = len(features)
	knowledge.Status.Took = metav1.Duration{Duration: time.Since(startedAt)}
	patch := client.MergeFrom(old)
	if err := r.Status().Patch(ctx, knowledge, patch); err != nil {
		log.Error(err, "failed to patch knowledge status")
		return ctrl.Result{}, err
	}
	log.Info("successfully extracted knowledge", "name", knowledge.Name, "took", knowledge.Status.Took.Duration)
	return ctrl.Result{}, nil
}

func (r *KnowledgeReconciler) SetupWithManager(mgr manager.Manager, mcl *multicluster.Client) error {
	return multicluster.BuildController(mcl, mgr).
		Named("cortex-knowledge").
		For(
			&v1alpha1.Knowledge{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				// Only react to datasources matching the operator.
				ds := obj.(*v1alpha1.Knowledge)
				return ds.Spec.SchedulingDomain == r.Conf.SchedulingDomain
			})),
		).
		Complete(r)
}
