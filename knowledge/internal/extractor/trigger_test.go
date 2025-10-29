// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"context"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/conf"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func setupTestReconciler(t *testing.T) (*TriggerReconciler, client.Client, context.Context) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 to scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add corev1 to scheme: %v", err)
	}

	testConf := conf.Config{
		Operator: "test-operator",
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	reconciler := &TriggerReconciler{
		Client: fakeClient,
		Scheme: scheme,
		Conf:   testConf,
	}

	return reconciler, fakeClient, ctx
}

func TestFindDependentKnowledge_DatasourceDependency(t *testing.T) {
	reconciler, fakeClient, ctx := setupTestReconciler(t)

	// Create a datasource
	datasource := &v1alpha1.Datasource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-datasource",
		},
		Spec: v1alpha1.DatasourceSpec{
			Operator: "test-operator",
			Type:     v1alpha1.DatasourceTypePrometheus,
		},
	}

	// Create knowledge that depends on the datasource
	knowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name: "dependent-knowledge",
		},
		Spec: v1alpha1.KnowledgeSpec{
			Operator: "test-operator",
			Dependencies: v1alpha1.KnowledgeDependenciesSpec{
				Datasources: []corev1.ObjectReference{
					{Name: "test-datasource"},
				},
			},
			Recency: metav1.Duration{Duration: time.Minute},
		},
	}

	// Create knowledge that doesn't depend on the datasource
	independentKnowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name: "independent-knowledge",
		},
		Spec: v1alpha1.KnowledgeSpec{
			Operator: "test-operator",
			Recency:  metav1.Duration{Duration: time.Minute},
		},
	}

	if err := fakeClient.Create(ctx, datasource); err != nil {
		t.Fatalf("Failed to create datasource: %v", err)
	}
	if err := fakeClient.Create(ctx, knowledge); err != nil {
		t.Fatalf("Failed to create knowledge: %v", err)
	}
	if err := fakeClient.Create(ctx, independentKnowledge); err != nil {
		t.Fatalf("Failed to create independent knowledge: %v", err)
	}

	dependents, err := reconciler.findDependentKnowledge(ctx, datasource)
	if err != nil {
		t.Fatalf("findDependentKnowledge failed: %v", err)
	}

	if len(dependents) != 1 {
		t.Fatalf("Expected 1 dependent, got %d", len(dependents))
	}
	if dependents[0].Name != "dependent-knowledge" {
		t.Errorf("Expected dependent name 'dependent-knowledge', got '%s'", dependents[0].Name)
	}
}

func TestFindDependentKnowledge_KnowledgeDependency(t *testing.T) {
	reconciler, fakeClient, ctx := setupTestReconciler(t)

	// Create a knowledge resource
	sourceKnowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name: "source-knowledge",
		},
		Spec: v1alpha1.KnowledgeSpec{
			Operator: "test-operator",
			Recency:  metav1.Duration{Duration: time.Minute},
		},
	}

	// Create knowledge that depends on the source knowledge
	dependentKnowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name: "dependent-knowledge",
		},
		Spec: v1alpha1.KnowledgeSpec{
			Operator: "test-operator",
			Dependencies: v1alpha1.KnowledgeDependenciesSpec{
				Knowledges: []corev1.ObjectReference{
					{Name: "source-knowledge"},
				},
			},
			Recency: metav1.Duration{Duration: time.Minute},
		},
	}

	if err := fakeClient.Create(ctx, sourceKnowledge); err != nil {
		t.Fatalf("Failed to create source knowledge: %v", err)
	}
	if err := fakeClient.Create(ctx, dependentKnowledge); err != nil {
		t.Fatalf("Failed to create dependent knowledge: %v", err)
	}

	dependents, err := reconciler.findDependentKnowledge(ctx, sourceKnowledge)
	if err != nil {
		t.Fatalf("findDependentKnowledge failed: %v", err)
	}

	if len(dependents) != 1 {
		t.Fatalf("Expected 1 dependent, got %d", len(dependents))
	}
	if dependents[0].Name != "dependent-knowledge" {
		t.Errorf("Expected dependent name 'dependent-knowledge', got '%s'", dependents[0].Name)
	}
}

func TestFindDependentKnowledge_OperatorFiltering(t *testing.T) {
	reconciler, fakeClient, ctx := setupTestReconciler(t)

	// Create a datasource
	datasource := &v1alpha1.Datasource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-datasource",
		},
		Spec: v1alpha1.DatasourceSpec{
			Operator: "test-operator",
			Type:     v1alpha1.DatasourceTypePrometheus,
		},
	}

	// Create knowledge for our operator
	ourKnowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name: "our-knowledge",
		},
		Spec: v1alpha1.KnowledgeSpec{
			Operator: "test-operator",
			Dependencies: v1alpha1.KnowledgeDependenciesSpec{
				Datasources: []corev1.ObjectReference{
					{Name: "test-datasource"},
				},
			},
			Recency: metav1.Duration{Duration: time.Minute},
		},
	}

	// Create knowledge for different operator
	otherKnowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name: "other-knowledge",
		},
		Spec: v1alpha1.KnowledgeSpec{
			Operator: "other-operator",
			Dependencies: v1alpha1.KnowledgeDependenciesSpec{
				Datasources: []corev1.ObjectReference{
					{Name: "test-datasource"},
				},
			},
			Recency: metav1.Duration{Duration: time.Minute},
		},
	}

	if err := fakeClient.Create(ctx, datasource); err != nil {
		t.Fatalf("Failed to create datasource: %v", err)
	}
	if err := fakeClient.Create(ctx, ourKnowledge); err != nil {
		t.Fatalf("Failed to create our knowledge: %v", err)
	}
	if err := fakeClient.Create(ctx, otherKnowledge); err != nil {
		t.Fatalf("Failed to create other knowledge: %v", err)
	}

	dependents, err := reconciler.findDependentKnowledge(ctx, datasource)
	if err != nil {
		t.Fatalf("findDependentKnowledge failed: %v", err)
	}

	if len(dependents) != 1 {
		t.Fatalf("Expected 1 dependent, got %d", len(dependents))
	}
	if dependents[0].Name != "our-knowledge" {
		t.Errorf("Expected dependent name 'our-knowledge', got '%s'", dependents[0].Name)
	}
}

func TestTriggerKnowledgeReconciliation_ImmediateTrigger(t *testing.T) {
	reconciler, fakeClient, ctx := setupTestReconciler(t)

	// Create knowledge that was last extracted longer ago than recency
	pastTime := time.Now().Add(-2 * time.Minute)
	knowledge := v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-knowledge",
		},
		Spec: v1alpha1.KnowledgeSpec{
			Operator: "test-operator",
			Recency:  metav1.Duration{Duration: time.Minute},
		},
		Status: v1alpha1.KnowledgeStatus{
			LastExtracted: metav1.NewTime(pastTime),
		},
	}

	if err := fakeClient.Create(ctx, &knowledge); err != nil {
		t.Fatalf("Failed to create knowledge: %v", err)
	}

	err := reconciler.triggerKnowledgeReconciliation(ctx, knowledge)
	if err != nil {
		t.Fatalf("triggerKnowledgeReconciliation failed: %v", err)
	}

	// Verify the knowledge was updated with trigger annotation
	var updatedKnowledge v1alpha1.Knowledge
	if err := fakeClient.Get(ctx, types.NamespacedName{Name: "test-knowledge"}, &updatedKnowledge); err != nil {
		t.Fatalf("Failed to get updated knowledge: %v", err)
	}

	if updatedKnowledge.Annotations == nil {
		t.Fatal("Expected annotations to be set")
	}
	if _, exists := updatedKnowledge.Annotations["cortex.knowledge/trigger-reconciliation"]; !exists {
		t.Error("Expected trigger annotation to be set")
	}
}

func TestTriggerKnowledgeReconciliation_ScheduledTrigger(t *testing.T) {
	reconciler, fakeClient, ctx := setupTestReconciler(t)

	// Create knowledge that was last extracted recently
	recentTime := time.Now().Add(-30 * time.Second)
	knowledge := v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-knowledge",
		},
		Spec: v1alpha1.KnowledgeSpec{
			Operator: "test-operator",
			Recency:  metav1.Duration{Duration: time.Minute},
		},
		Status: v1alpha1.KnowledgeStatus{
			LastExtracted: metav1.NewTime(recentTime),
		},
	}

	if err := fakeClient.Create(ctx, &knowledge); err != nil {
		t.Fatalf("Failed to create knowledge: %v", err)
	}

	err := reconciler.triggerKnowledgeReconciliation(ctx, knowledge)
	if err != nil {
		t.Fatalf("triggerKnowledgeReconciliation failed: %v", err)
	}

	// Verify the knowledge was updated with trigger annotation
	var updatedKnowledge v1alpha1.Knowledge
	if err := fakeClient.Get(ctx, types.NamespacedName{Name: "test-knowledge"}, &updatedKnowledge); err != nil {
		t.Fatalf("Failed to get updated knowledge: %v", err)
	}

	if updatedKnowledge.Annotations == nil {
		t.Fatal("Expected annotations to be set")
	}
	if _, exists := updatedKnowledge.Annotations["cortex.knowledge/trigger-reconciliation"]; !exists {
		t.Error("Expected trigger annotation to be set")
	}
}

func TestReconcile_DatasourceChanges(t *testing.T) {
	reconciler, fakeClient, ctx := setupTestReconciler(t)

	// Create a datasource
	datasource := &v1alpha1.Datasource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-datasource",
		},
		Spec: v1alpha1.DatasourceSpec{
			Operator: "test-operator",
			Type:     v1alpha1.DatasourceTypePrometheus,
		},
	}

	// Create knowledge that depends on the datasource
	pastTime := time.Now().Add(-2 * time.Minute)
	knowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name: "dependent-knowledge",
		},
		Spec: v1alpha1.KnowledgeSpec{
			Operator: "test-operator",
			Dependencies: v1alpha1.KnowledgeDependenciesSpec{
				Datasources: []corev1.ObjectReference{
					{Name: "test-datasource"},
				},
			},
			Recency: metav1.Duration{Duration: time.Minute},
		},
		Status: v1alpha1.KnowledgeStatus{
			LastExtracted: metav1.NewTime(pastTime),
		},
	}

	if err := fakeClient.Create(ctx, datasource); err != nil {
		t.Fatalf("Failed to create datasource: %v", err)
	}
	if err := fakeClient.Create(ctx, knowledge); err != nil {
		t.Fatalf("Failed to create knowledge: %v", err)
	}

	// Trigger reconcile for the datasource
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: "test-datasource",
		},
	}

	result, err := reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if result.Requeue {
		t.Error("Expected Requeue to be false")
	}

	// Verify the dependent knowledge was updated with trigger annotation
	var updatedKnowledge v1alpha1.Knowledge
	if err := fakeClient.Get(ctx, types.NamespacedName{Name: "dependent-knowledge"}, &updatedKnowledge); err != nil {
		t.Fatalf("Failed to get updated knowledge: %v", err)
	}

	if updatedKnowledge.Annotations == nil {
		t.Fatal("Expected annotations to be set")
	}
	if _, exists := updatedKnowledge.Annotations["cortex.knowledge/trigger-reconciliation"]; !exists {
		t.Error("Expected trigger annotation to be set")
	}
}

func TestReconcile_KnowledgeChanges(t *testing.T) {
	reconciler, fakeClient, ctx := setupTestReconciler(t)

	// Create source knowledge
	sourceKnowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name: "source-knowledge",
		},
		Spec: v1alpha1.KnowledgeSpec{
			Operator: "test-operator",
			Recency:  metav1.Duration{Duration: time.Minute},
		},
	}

	// Create dependent knowledge
	pastTime := time.Now().Add(-2 * time.Minute)
	dependentKnowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name: "dependent-knowledge",
		},
		Spec: v1alpha1.KnowledgeSpec{
			Operator: "test-operator",
			Dependencies: v1alpha1.KnowledgeDependenciesSpec{
				Knowledges: []corev1.ObjectReference{
					{Name: "source-knowledge"},
				},
			},
			Recency: metav1.Duration{Duration: time.Minute},
		},
		Status: v1alpha1.KnowledgeStatus{
			LastExtracted: metav1.NewTime(pastTime),
		},
	}

	if err := fakeClient.Create(ctx, sourceKnowledge); err != nil {
		t.Fatalf("Failed to create source knowledge: %v", err)
	}
	if err := fakeClient.Create(ctx, dependentKnowledge); err != nil {
		t.Fatalf("Failed to create dependent knowledge: %v", err)
	}

	// Trigger reconcile for the source knowledge
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: "source-knowledge",
		},
	}

	result, err := reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if result.Requeue {
		t.Error("Expected Requeue to be false")
	}

	// Verify the dependent knowledge was updated with trigger annotation
	var updatedKnowledge v1alpha1.Knowledge
	if err := fakeClient.Get(ctx, types.NamespacedName{Name: "dependent-knowledge"}, &updatedKnowledge); err != nil {
		t.Fatalf("Failed to get updated knowledge: %v", err)
	}

	if updatedKnowledge.Annotations == nil {
		t.Fatal("Expected annotations to be set")
	}
	if _, exists := updatedKnowledge.Annotations["cortex.knowledge/trigger-reconciliation"]; !exists {
		t.Error("Expected trigger annotation to be set")
	}
}

func TestReconcile_NonExistentResource(t *testing.T) {
	reconciler, _, ctx := setupTestReconciler(t)

	// Try to reconcile a non-existent resource
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: "non-existent",
		},
	}

	result, err := reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if result.Requeue {
		t.Error("Expected Requeue to be false")
	}
}

func TestGetResourceType_Datasource(t *testing.T) {
	datasource := &v1alpha1.Datasource{}
	resourceType := getResourceType(datasource)
	if resourceType != "Datasource" {
		t.Errorf("Expected 'Datasource', got '%s'", resourceType)
	}
}

func TestGetResourceType_Knowledge(t *testing.T) {
	knowledge := &v1alpha1.Knowledge{}
	resourceType := getResourceType(knowledge)
	if resourceType != "Knowledge" {
		t.Errorf("Expected 'Knowledge', got '%s'", resourceType)
	}
}

func TestGetResourceType_Unknown(t *testing.T) {
	secret := &corev1.Secret{}
	resourceType := getResourceType(secret)
	if resourceType != "Unknown" {
		t.Errorf("Expected 'Unknown', got '%s'", resourceType)
	}
}

func TestMapDatasourceToKnowledge_CorrectOperator(t *testing.T) {
	reconciler, _, ctx := setupTestReconciler(t)

	datasource := &v1alpha1.Datasource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-datasource",
		},
		Spec: v1alpha1.DatasourceSpec{
			Operator: "test-operator",
			Type:     v1alpha1.DatasourceTypePrometheus,
		},
	}

	requests := reconciler.mapDatasourceToKnowledge(ctx, datasource)
	if len(requests) != 1 {
		t.Fatalf("Expected 1 request, got %d", len(requests))
	}
	if requests[0].Name != "test-datasource" {
		t.Errorf("Expected request name 'test-datasource', got '%s'", requests[0].Name)
	}
}

func TestMapDatasourceToKnowledge_DifferentOperator(t *testing.T) {
	reconciler, _, ctx := setupTestReconciler(t)

	datasource := &v1alpha1.Datasource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-datasource",
		},
		Spec: v1alpha1.DatasourceSpec{
			Operator: "other-operator",
			Type:     v1alpha1.DatasourceTypePrometheus,
		},
	}

	requests := reconciler.mapDatasourceToKnowledge(ctx, datasource)
	if len(requests) != 0 {
		t.Errorf("Expected 0 requests, got %d", len(requests))
	}
}

func TestMapDatasourceToKnowledge_NonDatasource(t *testing.T) {
	reconciler, _, ctx := setupTestReconciler(t)

	secret := &corev1.Secret{}
	requests := reconciler.mapDatasourceToKnowledge(ctx, secret)
	if requests != nil {
		t.Errorf("Expected nil requests, got %v", requests)
	}
}

func TestMapKnowledgeToKnowledge_CorrectOperator(t *testing.T) {
	reconciler, _, ctx := setupTestReconciler(t)

	knowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-knowledge",
		},
		Spec: v1alpha1.KnowledgeSpec{
			Operator: "test-operator",
			Recency:  metav1.Duration{Duration: time.Minute},
		},
	}

	requests := reconciler.mapKnowledgeToKnowledge(ctx, knowledge)
	if len(requests) != 1 {
		t.Fatalf("Expected 1 request, got %d", len(requests))
	}
	if requests[0].Name != "test-knowledge" {
		t.Errorf("Expected request name 'test-knowledge', got '%s'", requests[0].Name)
	}
}

func TestMapKnowledgeToKnowledge_DifferentOperator(t *testing.T) {
	reconciler, _, ctx := setupTestReconciler(t)

	knowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-knowledge",
		},
		Spec: v1alpha1.KnowledgeSpec{
			Operator: "other-operator",
			Recency:  metav1.Duration{Duration: time.Minute},
		},
	}

	requests := reconciler.mapKnowledgeToKnowledge(ctx, knowledge)
	if len(requests) != 0 {
		t.Errorf("Expected 0 requests, got %d", len(requests))
	}
}

func TestMapKnowledgeToKnowledge_NonKnowledge(t *testing.T) {
	reconciler, _, ctx := setupTestReconciler(t)

	secret := &corev1.Secret{}
	requests := reconciler.mapKnowledgeToKnowledge(ctx, secret)
	if requests != nil {
		t.Errorf("Expected nil requests, got %v", requests)
	}
}
