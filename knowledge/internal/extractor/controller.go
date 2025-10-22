// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/extractor/plugins/kvm"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/extractor/plugins/netapp"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/extractor/plugins/sap"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/extractor/plugins/vmware"
	"github.com/cobaltcore-dev/cortex/lib/db"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type KnowledgeReconciler struct {
	// Client for the kubernetes API.
	client.Client
	// Kubernetes scheme to use for the deschedulings.
	Scheme *runtime.Scheme
	// Monitor to use for tracking the pipeline.
	monitor Monitor
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

	extractor, ok := map[string]plugins.FeatureExtractor{
		// VMware-specific extractors
		"vrops_hostsystem_resolver":                        &vmware.VROpsHostsystemResolver{},
		"vrops_project_noisiness_extractor":                &vmware.VROpsProjectNoisinessExtractor{},
		"vrops_hostsystem_contention_long_term_extractor":  &vmware.VROpsHostsystemContentionLongTermExtractor{},
		"vrops_hostsystem_contention_short_term_extractor": &vmware.VROpsHostsystemContentionShortTermExtractor{},
		// KVM-specific extractors
		"kvm_libvirt_domain_cpu_steal_pct_extractor": &kvm.LibvirtDomainCPUStealPctExtractor{},
		// NetApp-specific extractors
		"netapp_storage_pool_cpu_usage_extractor": &netapp.StoragePoolCPUUsageExtractor{},
		// Shared extractors
		"host_utilization_extractor":       &shared.HostUtilizationExtractor{},
		"host_capabilities_extractor":      &shared.HostCapabilitiesExtractor{},
		"vm_host_residency_extractor":      &shared.VMHostResidencyExtractor{},
		"vm_life_span_histogram_extractor": &shared.VMLifeSpanHistogramExtractor{},
		"host_az_extractor":                &shared.HostAZExtractor{},
		"host_pinned_projects_extractor":   &shared.HostPinnedProjectsExtractor{},
		// SAP-specific extractors
		"sap_host_details_extractor": &sap.HostDetailsExtractor{},
	}[knowledge.Spec.Extractor.Name]
	if !ok {
		log.Info("skipping knowledge extraction, unsupported extractor", "name", knowledge.Spec.Extractor.Name)
		knowledge.Status.Error = "unsupported extractor name: " + knowledge.Spec.Extractor.Name
		if err := r.Status().Update(ctx, knowledge); err != nil {
			log.Error(err, "failed to update knowledge status")
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
			knowledge.Status.Error = "failed to get datasource: " + err.Error()
			if err := r.Status().Update(ctx, knowledge); err != nil {
				log.Error(err, "failed to update knowledge status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}
		if databaseSecretRef == nil {
			databaseSecretRef = &ds.Spec.DatabaseSecretRef
		} else if databaseSecretRef.Name != ds.Spec.DatabaseSecretRef.Name ||
			databaseSecretRef.Namespace != ds.Spec.DatabaseSecretRef.Namespace {
			log.Error(nil, "datasources have differing database secret refs")
			knowledge.Status.Error = "datasources have differing database secret refs"
			if err := r.Status().Update(ctx, knowledge); err != nil {
				log.Error(err, "failed to update knowledge status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
	}
	// When we have datasources reading from a database, connect to it.
	var authenticatedDB *db.DB
	if databaseSecretRef != nil {
		authenticatedDB, err := db.Connector{Client: r.Client}.FromSecretRef(ctx, *databaseSecretRef)
		if err != nil {
			log.Error(err, "failed to authenticate with database", "secretRef", *databaseSecretRef)
			knowledge.Status.Error = "failed to authenticate with database: " + err.Error()
			if err := r.Status().Update(ctx, knowledge); err != nil {
				log.Error(err, "failed to update knowledge status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}
		defer authenticatedDB.Close()
	}

	// Initialize and run the extractor.
	// TODO: Wrap the monitor.
	if err := extractor.Init(authenticatedDB, knowledge.Spec); err != nil {
		log.Error(err, "failed to initialize feature extractor", "name", knowledge.Spec)
		knowledge.Status.Error = "failed to initialize feature extractor: " + err.Error()
		if err := r.Status().Update(ctx, knowledge); err != nil {
			log.Error(err, "failed to update knowledge status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	features, err := extractor.Extract()
	if err != nil {
		log.Error(err, "failed to extract features", "name", knowledge.Spec.Extractor.Name)
		knowledge.Status.Error = "failed to extract features: " + err.Error()
		if err := r.Status().Update(ctx, knowledge); err != nil {
			log.Error(err, "failed to update knowledge status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	// Update the knowledge status.
	raw := runtime.RawExtension{}
	raw.Raw, err = json.Marshal(features)
	if err != nil {
		log.Error(err, "failed to marshal extracted features", "name", knowledge.Spec.Extractor.Name)
		knowledge.Status.Error = "failed to marshal extracted features: " + err.Error()
		if err := r.Status().Update(ctx, knowledge); err != nil {
			log.Error(err, "failed to update knowledge status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}
	knowledge.Status.LastExtracted = metav1.NewTime(time.Now())
	knowledge.Status.Error = ""
	knowledge.Status.Raw = raw
	knowledge.Status.RawLength = len(features)
	knowledge.Status.Took = metav1.Duration{Duration: time.Since(startedAt)}
	if err := r.Status().Update(ctx, knowledge); err != nil {
		log.Error(err, "failed to update knowledge status")
		return ctrl.Result{}, err
	}
	log.Info("successfully extracted knowledge", "name", knowledge.Name, "took", knowledge.Status.Took.Duration)
	return ctrl.Result{}, nil
}
