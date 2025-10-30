// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/conf"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestKnowledgeReconciler_Reconcile_NonExistentResource(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := &KnowledgeReconciler{
		Client:  fakeClient,
		Scheme:  scheme,
		Monitor: NewMonitor(),
		Conf:    conf.Config{Operator: "test-operator"},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "non-existent-knowledge"},
	}

	result, err := reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if result.RequeueAfter > 0 {
		t.Error("Expected no requeue")
	}
}

func TestKnowledgeReconciler_Reconcile_SkipRecentExtraction(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := &KnowledgeReconciler{
		Client:  fakeClient,
		Scheme:  scheme,
		Monitor: NewMonitor(),
		Conf:    conf.Config{Operator: "test-operator"},
	}

	// Create knowledge that was extracted recently
	recentTime := time.Now().Add(-30 * time.Second)
	knowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{Name: "recent-knowledge"},
		Spec: v1alpha1.KnowledgeSpec{
			Operator: "test-operator",
			Recency:  metav1.Duration{Duration: time.Minute},
			Extractor: v1alpha1.KnowledgeExtractorSpec{
				Name: "host_utilization_extractor",
			},
		},
		Status: v1alpha1.KnowledgeStatus{
			LastExtracted: metav1.NewTime(recentTime),
		},
	}

	if err := fakeClient.Create(ctx, knowledge); err != nil {
		t.Fatal(err)
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "recent-knowledge"},
	}

	result, err := reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if result.RequeueAfter <= 0 {
		t.Error("Expected RequeueAfter to be greater than 0")
	}
}

func TestKnowledgeReconciler_Reconcile_UnsupportedExtractor(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	knowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{Name: "unsupported-extractor"},
		Spec: v1alpha1.KnowledgeSpec{
			Operator: "test-operator",
			Recency:  metav1.Duration{Duration: time.Minute},
			Extractor: v1alpha1.KnowledgeExtractorSpec{
				Name: "unsupported_extractor",
			},
		},
		Status: v1alpha1.KnowledgeStatus{
			LastExtracted: metav1.NewTime(time.Now().Add(-2 * time.Minute)),
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(knowledge).WithStatusSubresource(&v1alpha1.Knowledge{}).Build()
	reconciler := &KnowledgeReconciler{
		Client:  fakeClient,
		Scheme:  scheme,
		Monitor: NewMonitor(),
		Conf:    conf.Config{Operator: "test-operator"},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "unsupported-extractor"},
	}

	result, err := reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if result.RequeueAfter > 0 {
		t.Error("Expected no requeue")
	}

	// Verify the error status was set
	var updatedKnowledge v1alpha1.Knowledge
	if err := fakeClient.Get(ctx, types.NamespacedName{Name: "unsupported-extractor"}, &updatedKnowledge); err != nil {
		t.Fatal(err)
	}
	condition := meta.FindStatusCondition(updatedKnowledge.Status.Conditions, v1alpha1.KnowledgeConditionError)
	if condition == nil || !strings.Contains(condition.Message, "unsupported extractor name") {
		t.Errorf("Expected error to contain 'unsupported extractor name', got: %s", condition.Message)
	}
}

func TestKnowledgeReconciler_Reconcile_MissingDatasource(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	knowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{Name: "missing-datasource"},
		Spec: v1alpha1.KnowledgeSpec{
			Operator: "test-operator",
			Recency:  metav1.Duration{Duration: time.Minute},
			Extractor: v1alpha1.KnowledgeExtractorSpec{
				Name: "host_utilization_extractor",
			},
			Dependencies: v1alpha1.KnowledgeDependenciesSpec{
				Datasources: []corev1.ObjectReference{
					{Name: "missing-datasource"},
				},
			},
		},
		Status: v1alpha1.KnowledgeStatus{
			LastExtracted: metav1.NewTime(time.Now().Add(-2 * time.Minute)),
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(knowledge).WithStatusSubresource(&v1alpha1.Knowledge{}).Build()
	reconciler := &KnowledgeReconciler{
		Client:  fakeClient,
		Scheme:  scheme,
		Monitor: NewMonitor(),
		Conf:    conf.Config{Operator: "test-operator"},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "missing-datasource"},
	}

	result, err := reconciler.Reconcile(ctx, req)
	if err == nil {
		t.Error("Expected error for missing datasource, got nil")
	}
	if result.RequeueAfter > 0 {
		t.Error("Expected no requeue")
	}

	// Verify the error status was set
	var updatedKnowledge v1alpha1.Knowledge
	if err := fakeClient.Get(ctx, types.NamespacedName{Name: "missing-datasource"}, &updatedKnowledge); err != nil {
		t.Fatal(err)
	}
	condition := meta.FindStatusCondition(updatedKnowledge.Status.Conditions, v1alpha1.KnowledgeConditionError)
	if condition == nil || !strings.Contains(condition.Message, "failed to get datasource") {
		t.Errorf("Expected error to contain 'failed to get datasource', got: %s", condition.Message)
	}
}

func TestKnowledgeReconciler_Reconcile_DifferentDatabaseSecrets(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	// Create datasources with different database secret refs
	datasource1 := &v1alpha1.Datasource{
		ObjectMeta: metav1.ObjectMeta{Name: "datasource-1"},
		Spec: v1alpha1.DatasourceSpec{
			Operator:          "test-operator",
			Type:              v1alpha1.DatasourceTypePrometheus,
			DatabaseSecretRef: corev1.SecretReference{Name: "db-secret-1"},
		},
	}

	datasource2 := &v1alpha1.Datasource{
		ObjectMeta: metav1.ObjectMeta{Name: "datasource-2"},
		Spec: v1alpha1.DatasourceSpec{
			Operator:          "test-operator",
			Type:              v1alpha1.DatasourceTypePrometheus,
			DatabaseSecretRef: corev1.SecretReference{Name: "db-secret-2"},
		},
	}

	knowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{Name: "different-db-secrets"},
		Spec: v1alpha1.KnowledgeSpec{
			Operator: "test-operator",
			Recency:  metav1.Duration{Duration: time.Minute},
			Extractor: v1alpha1.KnowledgeExtractorSpec{
				Name: "host_utilization_extractor",
			},
			Dependencies: v1alpha1.KnowledgeDependenciesSpec{
				Datasources: []corev1.ObjectReference{
					{Name: "datasource-1"},
					{Name: "datasource-2"},
				},
			},
		},
		Status: v1alpha1.KnowledgeStatus{
			LastExtracted: metav1.NewTime(time.Now().Add(-2 * time.Minute)),
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(datasource1, datasource2, knowledge).WithStatusSubresource(&v1alpha1.Knowledge{}).Build()
	reconciler := &KnowledgeReconciler{
		Client:  fakeClient,
		Scheme:  scheme,
		Monitor: NewMonitor(),
		Conf:    conf.Config{Operator: "test-operator"},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "different-db-secrets"},
	}

	_, err := reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Verify the error status was set
	var updatedKnowledge v1alpha1.Knowledge
	if err := fakeClient.Get(ctx, types.NamespacedName{Name: "different-db-secrets"}, &updatedKnowledge); err != nil {
		t.Fatal(err)
	}
	condition := meta.FindStatusCondition(updatedKnowledge.Status.Conditions, v1alpha1.KnowledgeConditionError)
	if condition == nil || !strings.Contains(condition.Message, "datasources have differing database secret refs") {
		t.Errorf("Expected error to contain 'datasources have differing database secret refs', got: %s", condition.Message)
	}
}

func TestKnowledgeReconciler_Reconcile_SameDatabaseSecrets(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	// Create datasources with the same database secret ref
	datasource1 := &v1alpha1.Datasource{
		ObjectMeta: metav1.ObjectMeta{Name: "datasource-1"},
		Spec: v1alpha1.DatasourceSpec{
			Operator:          "test-operator",
			Type:              v1alpha1.DatasourceTypePrometheus,
			DatabaseSecretRef: corev1.SecretReference{Name: "shared-db-secret"},
		},
	}

	datasource2 := &v1alpha1.Datasource{
		ObjectMeta: metav1.ObjectMeta{Name: "datasource-2"},
		Spec: v1alpha1.DatasourceSpec{
			Operator:          "test-operator",
			Type:              v1alpha1.DatasourceTypePrometheus,
			DatabaseSecretRef: corev1.SecretReference{Name: "shared-db-secret"},
		},
	}

	knowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{Name: "same-db-secrets"},
		Spec: v1alpha1.KnowledgeSpec{
			Operator: "test-operator",
			Recency:  metav1.Duration{Duration: time.Minute},
			Extractor: v1alpha1.KnowledgeExtractorSpec{
				Name: "host_utilization_extractor",
			},
			Dependencies: v1alpha1.KnowledgeDependenciesSpec{
				Datasources: []corev1.ObjectReference{
					{Name: "datasource-1"},
					{Name: "datasource-2"},
				},
			},
		},
		Status: v1alpha1.KnowledgeStatus{
			LastExtracted: metav1.NewTime(time.Now().Add(-2 * time.Minute)),
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(datasource1, datasource2, knowledge).WithStatusSubresource(&v1alpha1.Knowledge{}).Build()
	reconciler := &KnowledgeReconciler{
		Client:  fakeClient,
		Scheme:  scheme,
		Monitor: NewMonitor(),
		Conf:    conf.Config{Operator: "test-operator"},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "same-db-secrets"},
	}

	_, err := reconciler.Reconcile(ctx, req)
	// Expect an error because the secret doesn't exist
	if err == nil {
		t.Error("Expected error for missing secret, got nil")
	}

	// The reconciliation should fail due to missing secret,
	// but it should pass the database secret ref validation
	var updatedKnowledge v1alpha1.Knowledge
	if err := fakeClient.Get(ctx, types.NamespacedName{Name: "same-db-secrets"}, &updatedKnowledge); err != nil {
		t.Fatal(err)
	}
	// Should not have the "differing database secret refs" error
	condition := meta.FindStatusCondition(updatedKnowledge.Status.Conditions, v1alpha1.KnowledgeConditionError)
	if condition == nil || strings.Contains(condition.Message, "datasources have differing database secret refs") {
		t.Errorf("Should not have 'differing database secret refs' error, got: %s", condition.Message)
	}
	// Should have an error about authentication failure or missing secret
	if condition == nil || (!strings.Contains(condition.Message, "failed to authenticate with database") &&
		!strings.Contains(condition.Message, "secret not found")) {
		t.Errorf("Expected error to contain 'failed to authenticate with database' or 'secret not found', got: %s", condition.Message)
	}
}

func TestKnowledgeReconciler_Reconcile_NoDatasources(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	knowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{Name: "no-datasources"},
		Spec: v1alpha1.KnowledgeSpec{
			Operator: "test-operator",
			Recency:  metav1.Duration{Duration: time.Minute},
			Extractor: v1alpha1.KnowledgeExtractorSpec{
				Name: "host_utilization_extractor",
			},
		},
		Status: v1alpha1.KnowledgeStatus{
			LastExtracted: metav1.NewTime(time.Now().Add(-2 * time.Minute)),
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(knowledge).WithStatusSubresource(&v1alpha1.Knowledge{}).Build()
	reconciler := &KnowledgeReconciler{
		Client:  fakeClient,
		Scheme:  scheme,
		Monitor: NewMonitor(),
		Conf:    conf.Config{Operator: "test-operator"},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "no-datasources"},
	}

	_, err := reconciler.Reconcile(ctx, req)
	// Expect an error because feature extraction will fail without database connection
	if err == nil {
		t.Error("Expected error for missing database connection, got nil")
	}

	// Should proceed to feature extraction (which will fail due to missing database connection)
	var updatedKnowledge v1alpha1.Knowledge
	if err := fakeClient.Get(ctx, types.NamespacedName{Name: "no-datasources"}, &updatedKnowledge); err != nil {
		t.Fatal(err)
	}
	// Should have an error related to feature extraction, not datasource validation
	condition := meta.FindStatusCondition(updatedKnowledge.Status.Conditions, v1alpha1.KnowledgeConditionError)
	if condition == nil || !strings.Contains(condition.Message, "failed to initialize feature extractor") &&
		!strings.Contains(condition.Message, "database connection is not initialized") {
		t.Errorf("Expected error to contain 'failed to initialize feature extractor' or 'database connection is not initialized', got: %s", condition.Message)
	}
}

func TestKnowledgeReconciler_Reconcile_SupportedExtractors(t *testing.T) {
	supportedExtractors := []string{
		"vrops_hostsystem_resolver",
		"vrops_project_noisiness_extractor",
		"vrops_hostsystem_contention_long_term_extractor",
		"vrops_hostsystem_contention_short_term_extractor",
		"kvm_libvirt_domain_cpu_steal_pct_extractor",
		"netapp_storage_pool_cpu_usage_extractor",
		"host_utilization_extractor",
		"host_capabilities_extractor",
		"vm_host_residency_extractor",
		"vm_life_span_histogram_extractor",
		"host_az_extractor",
		"host_pinned_projects_extractor",
		"sap_host_details_extractor",
	}

	for _, extractorName := range supportedExtractors {
		t.Run("Extractor_"+extractorName, func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			if err := v1alpha1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}

			knowledge := &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{Name: "test-" + extractorName},
				Spec: v1alpha1.KnowledgeSpec{
					Operator: "test-operator",
					Recency:  metav1.Duration{Duration: time.Minute},
					Extractor: v1alpha1.KnowledgeExtractorSpec{
						Name: extractorName,
					},
				},
				Status: v1alpha1.KnowledgeStatus{
					LastExtracted: metav1.NewTime(time.Now().Add(-2 * time.Minute)),
				},
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(knowledge).WithStatusSubresource(&v1alpha1.Knowledge{}).Build()
			reconciler := &KnowledgeReconciler{
				Client:  fakeClient,
				Scheme:  scheme,
				Monitor: NewMonitor(),
				Conf:    conf.Config{Operator: "test-operator"},
			}

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{Name: "test-" + extractorName},
			}

			_, err := reconciler.Reconcile(ctx, req)
			// Expect an error because extractors will fail without database connection
			if err == nil {
				t.Error("Expected error for missing database connection, got nil")
			}

			// Should not fail with unsupported extractor error
			var updatedKnowledge v1alpha1.Knowledge
			if err := fakeClient.Get(ctx, types.NamespacedName{Name: "test-" + extractorName}, &updatedKnowledge); err != nil {
				t.Fatal(err)
			}
			condition := meta.FindStatusCondition(updatedKnowledge.Status.Conditions, v1alpha1.KnowledgeConditionError)
			if condition == nil || strings.Contains(condition.Message, "unsupported extractor name") {
				t.Errorf("Should not have 'unsupported extractor name' error, got: %s", condition.Message)
			}
			// Should have an error related to feature extraction or database connection
			if condition == nil || (!strings.Contains(condition.Message, "failed to initialize feature extractor") &&
				!strings.Contains(condition.Message, "database connection is not initialized")) {
				t.Errorf("Expected error to contain 'failed to initialize feature extractor' or 'database connection is not initialized', got: %s", condition.Message)
			}
		})
	}
}

func TestKnowledgeReconciler_OperatorFiltering(t *testing.T) {
	reconciler := &KnowledgeReconciler{
		Conf: conf.Config{Operator: "test-operator"},
	}

	// Test the predicate function logic
	predicateFunc := func(obj client.Object) bool {
		k := obj.(*v1alpha1.Knowledge)
		return k.Spec.Operator == reconciler.Conf.Operator
	}

	knowledge1 := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{Name: "our-operator-knowledge"},
		Spec: v1alpha1.KnowledgeSpec{
			Operator: "test-operator", // Our operator
		},
	}

	knowledge2 := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{Name: "other-operator-knowledge"},
		Spec: v1alpha1.KnowledgeSpec{
			Operator: "other-operator", // Different operator
		},
	}

	if !predicateFunc(knowledge1) {
		t.Error("Expected knowledge1 to pass predicate filter")
	}
	if predicateFunc(knowledge2) {
		t.Error("Expected knowledge2 to fail predicate filter")
	}
}
