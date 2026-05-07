// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestOpenStackDatasourceReconciler_Creation(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 to scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add corev1 to scheme: %v", err)
	}

	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler := &OpenStackDatasourceReconciler{
		Client:  client,
		Scheme:  scheme,
		Monitor: datasources.Monitor{},
		conf:    config{SchedulingDomain: "test-operator"},
	}

	if reconciler.Client == nil {
		t.Error("Client should not be nil")
	}

	if reconciler.Scheme == nil {
		t.Error("Scheme should not be nil")
	}

	if reconciler.conf.SchedulingDomain != "test-operator" {
		t.Errorf("Expected scheduling domain 'test-operator', got %s", reconciler.conf.SchedulingDomain)
	}
}

func TestOpenStackDatasourceTypes(t *testing.T) {
	// Test Nova datasource
	nova := v1alpha1.NovaDatasource{
		Type:                              v1alpha1.NovaDatasourceTypeServers,
		DeletedServersChangesSinceMinutes: nil,
	}

	if nova.Type != v1alpha1.NovaDatasourceTypeServers {
		t.Errorf("Expected type %s, got %s", v1alpha1.NovaDatasourceTypeServers, nova.Type)
	}

	// Test with deleted servers config
	deletedMinutes := 60
	nova.Type = v1alpha1.NovaDatasourceTypeDeletedServers
	nova.DeletedServersChangesSinceMinutes = &deletedMinutes

	if nova.Type != v1alpha1.NovaDatasourceTypeDeletedServers {
		t.Errorf("Expected type %s, got %s", v1alpha1.NovaDatasourceTypeDeletedServers, nova.Type)
	}

	if nova.DeletedServersChangesSinceMinutes == nil || *nova.DeletedServersChangesSinceMinutes != 60 {
		t.Errorf("Expected DeletedServersChangesSinceMinutes 60, got %v", nova.DeletedServersChangesSinceMinutes)
	}
}

func TestOpenStackDatasourceTypeConstants(t *testing.T) {
	tests := []struct {
		constant v1alpha1.OpenStackDatasourceType
		expected string
	}{
		{v1alpha1.OpenStackDatasourceTypeNova, "nova"},
		{v1alpha1.OpenStackDatasourceTypePlacement, "placement"},
		{v1alpha1.OpenStackDatasourceTypeManila, "manila"},
		{v1alpha1.OpenStackDatasourceTypeIdentity, "identity"},
		{v1alpha1.OpenStackDatasourceTypeLimes, "limes"},
		{v1alpha1.OpenStackDatasourceTypeCinder, "cinder"},
	}

	for _, test := range tests {
		if string(test.constant) != test.expected {
			t.Errorf("Expected %s constant to be '%s', got '%s'", test.expected, test.expected, string(test.constant))
		}
	}
}

func TestNovaDatasourceTypeConstants(t *testing.T) {
	tests := []struct {
		constant v1alpha1.NovaDatasourceType
		expected string
	}{
		{v1alpha1.NovaDatasourceTypeServers, "servers"},
		{v1alpha1.NovaDatasourceTypeDeletedServers, "deletedServers"},
		{v1alpha1.NovaDatasourceTypeHypervisors, "hypervisors"},
		{v1alpha1.NovaDatasourceTypeFlavors, "flavors"},
		{v1alpha1.NovaDatasourceTypeMigrations, "migrations"},
		{v1alpha1.NovaDatasourceTypeAggregates, "aggregates"},
		{v1alpha1.NovaDatasourceTypeImages, "images"},
	}

	for _, test := range tests {
		if string(test.constant) != test.expected {
			t.Errorf("Expected %s constant to be '%s', got '%s'", test.expected, test.expected, string(test.constant))
		}
	}
}

func TestPlacementDatasourceTypeConstants(t *testing.T) {
	tests := []struct {
		constant v1alpha1.PlacementDatasourceType
		expected string
	}{
		{v1alpha1.PlacementDatasourceTypeResourceProviders, "resourceProviders"},
		{v1alpha1.PlacementDatasourceTypeResourceProviderInventoryUsages, "resourceProviderInventoryUsages"},
		{v1alpha1.PlacementDatasourceTypeResourceProviderTraits, "resourceProviderTraits"},
	}

	for _, test := range tests {
		if string(test.constant) != test.expected {
			t.Errorf("Expected %s constant to be '%s', got '%s'", test.expected, test.expected, string(test.constant))
		}
	}
}

func TestOpenStackDatasourceSpec(t *testing.T) {
	// Test creating a complete Nova datasource spec
	spec := v1alpha1.DatasourceSpec{
		Type: v1alpha1.DatasourceTypeOpenStack,
		OpenStack: v1alpha1.OpenStackDatasource{
			Type: v1alpha1.OpenStackDatasourceTypeNova,
			Nova: v1alpha1.NovaDatasource{
				Type: v1alpha1.NovaDatasourceTypeServers,
			},
			SyncInterval: metav1.Duration{Duration: 3600 * time.Second},
			SecretRef: corev1.SecretReference{
				Name:      "keystone-credentials",
				Namespace: "openstack",
			},
		},
		DatabaseSecretRef: corev1.SecretReference{
			Name:      "db-credentials",
			Namespace: "default",
		},
	}

	if spec.Type != v1alpha1.DatasourceTypeOpenStack {
		t.Errorf("Expected type %s, got %s", v1alpha1.DatasourceTypeOpenStack, spec.Type)
	}

	if spec.OpenStack.Type != v1alpha1.OpenStackDatasourceTypeNova {
		t.Errorf("Expected OpenStack type %s, got %s", v1alpha1.OpenStackDatasourceTypeNova, spec.OpenStack.Type)
	}

	if spec.OpenStack.Nova.Type != v1alpha1.NovaDatasourceTypeServers {
		t.Errorf("Expected Nova type %s, got %s", v1alpha1.NovaDatasourceTypeServers, spec.OpenStack.Nova.Type)
	}

	if spec.OpenStack.SyncInterval.Seconds() != 3600 {
		t.Errorf("Expected SyncInterval 3600, got %f", spec.OpenStack.SyncInterval.Seconds())
	}

	if spec.OpenStack.SecretRef.Name != "keystone-credentials" {
		t.Errorf("Expected SecretRef.Name 'keystone-credentials', got %s", spec.OpenStack.SecretRef.Name)
	}
}

func TestManilaDatasourceSpec(t *testing.T) {
	spec := v1alpha1.DatasourceSpec{
		OpenStack: v1alpha1.OpenStackDatasource{
			Type: v1alpha1.OpenStackDatasourceTypeManila,
			Manila: v1alpha1.ManilaDatasource{
				Type: v1alpha1.ManilaDatasourceTypeStoragePools,
			},
			SyncInterval: metav1.Duration{Duration: 1800 * time.Second},
			SecretRef: corev1.SecretReference{
				Name:      "keystone-secret",
				Namespace: "manila",
			},
		},
	}

	if spec.OpenStack.Type != v1alpha1.OpenStackDatasourceTypeManila {
		t.Errorf("Expected type %s, got %s", v1alpha1.OpenStackDatasourceTypeManila, spec.OpenStack.Type)
	}

	if spec.OpenStack.Manila.Type != v1alpha1.ManilaDatasourceTypeStoragePools {
		t.Errorf("Expected Manila type %s, got %s", v1alpha1.ManilaDatasourceTypeStoragePools, spec.OpenStack.Manila.Type)
	}
}

func TestPlacementDatasourceSpec(t *testing.T) {
	spec := v1alpha1.DatasourceSpec{
		OpenStack: v1alpha1.OpenStackDatasource{
			Type: v1alpha1.OpenStackDatasourceTypePlacement,
			Placement: v1alpha1.PlacementDatasource{
				Type: v1alpha1.PlacementDatasourceTypeResourceProviders,
			},
			SyncInterval: metav1.Duration{Duration: 900 * time.Second},
			SecretRef: corev1.SecretReference{
				Name:      "keystone-secret",
				Namespace: "placement",
			},
		},
	}

	if spec.OpenStack.Type != v1alpha1.OpenStackDatasourceTypePlacement {
		t.Errorf("Expected type %s, got %s", v1alpha1.OpenStackDatasourceTypePlacement, spec.OpenStack.Type)
	}

	if spec.OpenStack.Placement.Type != v1alpha1.PlacementDatasourceTypeResourceProviders {
		t.Errorf("Expected Placement type %s, got %s", v1alpha1.PlacementDatasourceTypeResourceProviders, spec.OpenStack.Placement.Type)
	}
}

func TestIdentityDatasourceSpec(t *testing.T) {
	spec := v1alpha1.DatasourceSpec{
		OpenStack: v1alpha1.OpenStackDatasource{
			Type: v1alpha1.OpenStackDatasourceTypeIdentity,
			Identity: v1alpha1.IdentityDatasource{
				Type: v1alpha1.IdentityDatasourceTypeProjects,
			},
			SyncInterval: metav1.Duration{Duration: 7200 * time.Second},
			SecretRef: corev1.SecretReference{
				Name:      "keystone-secret",
				Namespace: "identity",
			},
		},
	}

	if spec.OpenStack.Type != v1alpha1.OpenStackDatasourceTypeIdentity {
		t.Errorf("Expected type %s, got %s", v1alpha1.OpenStackDatasourceTypeIdentity, spec.OpenStack.Type)
	}

	if spec.OpenStack.Identity.Type != v1alpha1.IdentityDatasourceTypeProjects {
		t.Errorf("Expected Identity type %s, got %s", v1alpha1.IdentityDatasourceTypeProjects, spec.OpenStack.Identity.Type)
	}
}

func TestLimesDatasourceSpec(t *testing.T) {
	spec := v1alpha1.DatasourceSpec{
		OpenStack: v1alpha1.OpenStackDatasource{
			Type: v1alpha1.OpenStackDatasourceTypeLimes,
			Limes: v1alpha1.LimesDatasource{
				Type: v1alpha1.LimesDatasourceTypeProjectCommitments,
			},
			SyncInterval: metav1.Duration{Duration: 14400 * time.Second},
			SecretRef: corev1.SecretReference{
				Name:      "keystone-secret",
				Namespace: "limes",
			},
		},
	}

	if spec.OpenStack.Type != v1alpha1.OpenStackDatasourceTypeLimes {
		t.Errorf("Expected type %s, got %s", v1alpha1.OpenStackDatasourceTypeLimes, spec.OpenStack.Type)
	}

	if spec.OpenStack.Limes.Type != v1alpha1.LimesDatasourceTypeProjectCommitments {
		t.Errorf("Expected Limes type %s, got %s", v1alpha1.LimesDatasourceTypeProjectCommitments, spec.OpenStack.Limes.Type)
	}
}

func TestCinderDatasourceSpec(t *testing.T) {
	spec := v1alpha1.DatasourceSpec{
		OpenStack: v1alpha1.OpenStackDatasource{
			Type: v1alpha1.OpenStackDatasourceTypeCinder,
			Cinder: v1alpha1.CinderDatasource{
				Type: v1alpha1.CinderDatasourceTypeStoragePools,
			},
			SyncInterval: metav1.Duration{Duration: 1800 * time.Second},
			SecretRef: corev1.SecretReference{
				Name:      "keystone-secret",
				Namespace: "cinder",
			},
		},
	}

	if spec.OpenStack.Type != v1alpha1.OpenStackDatasourceTypeCinder {
		t.Errorf("Expected type %s, got %s", v1alpha1.OpenStackDatasourceTypeCinder, spec.OpenStack.Type)
	}

	if spec.OpenStack.Cinder.Type != v1alpha1.CinderDatasourceTypeStoragePools {
		t.Errorf("Expected Cinder type %s, got %s", v1alpha1.CinderDatasourceTypeStoragePools, spec.OpenStack.Cinder.Type)
	}
}

func TestOpenStackDatasourceValidation(t *testing.T) {
	tests := []struct {
		name       string
		datasource v1alpha1.Datasource
		valid      bool
	}{
		{
			name: "valid nova datasource",
			datasource: v1alpha1.Datasource{
				Spec: v1alpha1.DatasourceSpec{
					Type: v1alpha1.DatasourceTypeOpenStack,
					OpenStack: v1alpha1.OpenStackDatasource{
						Type: v1alpha1.OpenStackDatasourceTypeNova,
						Nova: v1alpha1.NovaDatasource{
							Type: v1alpha1.NovaDatasourceTypeServers,
						},
						SyncInterval: metav1.Duration{Duration: 3600 * time.Second},
						SecretRef: corev1.SecretReference{
							Name:      "keystone-secret",
							Namespace: "default",
						},
					},
				},
			},
			valid: true,
		},
		{
			name: "valid placement datasource",
			datasource: v1alpha1.Datasource{
				Spec: v1alpha1.DatasourceSpec{
					Type: v1alpha1.DatasourceTypeOpenStack,
					OpenStack: v1alpha1.OpenStackDatasource{
						Type: v1alpha1.OpenStackDatasourceTypePlacement,
						Placement: v1alpha1.PlacementDatasource{
							Type: v1alpha1.PlacementDatasourceTypeResourceProviders,
						},
						SyncInterval: metav1.Duration{Duration: 1800 * time.Second},
						SecretRef: corev1.SecretReference{
							Name:      "keystone-secret",
							Namespace: "default",
						},
					},
				},
			},
			valid: true,
		},
		{
			name: "missing secret ref",
			datasource: v1alpha1.Datasource{
				Spec: v1alpha1.DatasourceSpec{
					Type: v1alpha1.DatasourceTypeOpenStack,
					OpenStack: v1alpha1.OpenStackDatasource{
						Type: v1alpha1.OpenStackDatasourceTypeNova,
						Nova: v1alpha1.NovaDatasource{
							Type: v1alpha1.NovaDatasourceTypeServers,
						},
						SyncInterval: metav1.Duration{Duration: 3600 * time.Second},
						// Missing SecretRef
					},
				},
			},
			valid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Basic validation - check that required fields are present
			if tt.datasource.Spec.Type != v1alpha1.DatasourceTypeOpenStack && tt.valid {
				t.Errorf("Expected valid datasource to have OpenStack type")
			}

			if tt.valid && tt.datasource.Spec.OpenStack.SecretRef.Name == "" {
				t.Errorf("Expected valid datasource to have secret reference")
			}

			if tt.valid && tt.datasource.Spec.OpenStack.SyncInterval.Seconds() <= 0 {
				t.Errorf("Expected valid datasource to have positive sync interval")
			}
		})
	}
}

func TestOpenStackDatasourceTypeMapping(t *testing.T) {
	// Test that all OpenStack datasource types are covered
	supportedTypes := []v1alpha1.OpenStackDatasourceType{
		v1alpha1.OpenStackDatasourceTypeNova,
		v1alpha1.OpenStackDatasourceTypePlacement,
		v1alpha1.OpenStackDatasourceTypeManila,
		v1alpha1.OpenStackDatasourceTypeIdentity,
		v1alpha1.OpenStackDatasourceTypeLimes,
		v1alpha1.OpenStackDatasourceTypeCinder,
	}

	for _, dsType := range supportedTypes {
		t.Run("type_"+string(dsType), func(t *testing.T) {
			spec := v1alpha1.OpenStackDatasource{
				Type: dsType,
				SecretRef: corev1.SecretReference{
					Name:      "test-secret",
					Namespace: "default",
				},
			}

			if spec.Type != dsType {
				t.Errorf("Expected type %s, got %s", dsType, spec.Type)
			}
		})
	}
}

func TestErrorHandling(t *testing.T) {
	// Test that we can handle the dependency error
	if v1alpha1.ErrWaitingForDependencyDatasource == nil {
		t.Error("Expected ErrWaitingForDependencyDatasource to be defined")
	}

	expectedMessage := "waiting for dependency datasource to become available"
	if v1alpha1.ErrWaitingForDependencyDatasource.Error() != expectedMessage {
		t.Errorf("Expected error message '%s', got '%s'", expectedMessage, v1alpha1.ErrWaitingForDependencyDatasource.Error())
	}
}

func TestOpenStackDatasourceDefaults(t *testing.T) {
	// Test default values for sync interval
	ds := v1alpha1.OpenStackDatasource{
		Type: v1alpha1.OpenStackDatasourceTypeNova,
		SecretRef: corev1.SecretReference{
			Name:      "test-secret",
			Namespace: "default",
		},
		// SyncIntervalSeconds not set - should use default
	}

	// The default should be set via kubebuilder annotations, but we can test the type
	if ds.Type != v1alpha1.OpenStackDatasourceTypeNova {
		t.Errorf("Expected type %s, got %s", v1alpha1.OpenStackDatasourceTypeNova, ds.Type)
	}
}

func TestUpdatePredicateIgnoresStatusConditionChanges(t *testing.T) {
	tests := []struct {
		name     string
		oldObj   *v1alpha1.Datasource
		newObj   *v1alpha1.Datasource
		expected bool
	}{
		{
			name: "only status conditions change - should not trigger reconcile",
			oldObj: &v1alpha1.Datasource{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
				Spec:       v1alpha1.DatasourceSpec{Type: v1alpha1.DatasourceTypeOpenStack},
				Status: v1alpha1.DatasourceStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha1.DatasourceConditionReady, Status: metav1.ConditionFalse},
					},
				},
			},
			newObj: &v1alpha1.Datasource{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
				Spec:       v1alpha1.DatasourceSpec{Type: v1alpha1.DatasourceTypeOpenStack},
				Status: v1alpha1.DatasourceStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha1.DatasourceConditionReady, Status: metav1.ConditionTrue},
					},
				},
			},
			expected: false,
		},
		{
			name: "spec stays equal - should not trigger reconcile",
			oldObj: &v1alpha1.Datasource{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
				Spec:       v1alpha1.DatasourceSpec{Type: v1alpha1.DatasourceTypeOpenStack},
			},
			newObj: &v1alpha1.Datasource{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
				Spec:       v1alpha1.DatasourceSpec{Type: v1alpha1.DatasourceTypeOpenStack},
			},
			expected: false,
		},
		{
			name: "metadata labels change - should trigger reconcile",
			oldObj: &v1alpha1.Datasource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
					Labels:    map[string]string{"env": "dev"},
				},
			},
			newObj: &v1alpha1.Datasource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
					Labels:    map[string]string{"env": "prod"},
				},
			},
			expected: true,
		},
		{
			name: "status NumberOfObjects changes - should trigger reconcile",
			oldObj: &v1alpha1.Datasource{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
				Status:     v1alpha1.DatasourceStatus{NumberOfObjects: 10},
			},
			newObj: &v1alpha1.Datasource{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
				Status:     v1alpha1.DatasourceStatus{NumberOfObjects: 20},
			},
			expected: true,
		},
		{
			name: "both conditions and other fields change - should trigger reconcile",
			oldObj: &v1alpha1.Datasource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
					Labels:    map[string]string{"version": "v1"},
				},
				Status: v1alpha1.DatasourceStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha1.DatasourceConditionReady, Status: metav1.ConditionFalse},
					},
				},
			},
			newObj: &v1alpha1.Datasource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
					Labels:    map[string]string{"version": "v2"},
				},
				Status: v1alpha1.DatasourceStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha1.DatasourceConditionReady, Status: metav1.ConditionTrue},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := event.UpdateEvent{
				ObjectOld: tt.oldObj,
				ObjectNew: tt.newObj,
			}
			result := predicateIgnoreStatusConditions().Update(e)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}
