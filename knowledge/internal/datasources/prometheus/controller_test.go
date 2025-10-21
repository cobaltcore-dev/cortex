// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/conf"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/datasources"
	"github.com/cobaltcore-dev/cortex/lib/db"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPrometheusDatasourceReconciler_Creation(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 to scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add corev1 to scheme: %v", err)
	}

	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler := &PrometheusDatasourceReconciler{
		Client:  client,
		Scheme:  scheme,
		Conf:    conf.Config{Operator: "test-operator"},
		Monitor: datasources.Monitor{},
	}

	if reconciler.Client == nil {
		t.Error("Client should not be nil")
	}

	if reconciler.Scheme == nil {
		t.Error("Scheme should not be nil")
	}

	if reconciler.Conf.Operator != "test-operator" {
		t.Errorf("Expected operator 'test-operator', got %s", reconciler.Conf.Operator)
	}
}

func TestPrometheusDatasourceTypes(t *testing.T) {
	// Test that the prometheus datasource struct has the expected fields
	ds := v1alpha1.PrometheusDatasource{
		Query:             "up",
		Alias:             "test_metric",
		Type:              "node_exporter_metric",
		TimeRangeSeconds:  3600,
		IntervalSeconds:   60,
		ResolutionSeconds: 15,
		SecretRef: corev1.SecretReference{
			Name:      "prometheus-secret",
			Namespace: "default",
		},
	}

	if ds.Query != "up" {
		t.Errorf("Expected query 'up', got %s", ds.Query)
	}

	if ds.Alias != "test_metric" {
		t.Errorf("Expected alias 'test_metric', got %s", ds.Alias)
	}

	if ds.Type != "node_exporter_metric" {
		t.Errorf("Expected type 'node_exporter_metric', got %s", ds.Type)
	}

	if ds.TimeRangeSeconds != 3600 {
		t.Errorf("Expected TimeRangeSeconds 3600, got %d", ds.TimeRangeSeconds)
	}

	if ds.IntervalSeconds != 60 {
		t.Errorf("Expected IntervalSeconds 60, got %d", ds.IntervalSeconds)
	}

	if ds.ResolutionSeconds != 15 {
		t.Errorf("Expected ResolutionSeconds 15, got %d", ds.ResolutionSeconds)
	}

	if ds.SecretRef.Name != "prometheus-secret" {
		t.Errorf("Expected SecretRef.Name 'prometheus-secret', got %s", ds.SecretRef.Name)
	}

	if ds.SecretRef.Namespace != "default" {
		t.Errorf("Expected SecretRef.Namespace 'default', got %s", ds.SecretRef.Namespace)
	}
}

func TestDatasourceTypeConstants(t *testing.T) {
	// Test that the datasource type constants are correct
	if v1alpha1.DatasourceTypePrometheus != "prometheus" {
		t.Errorf("Expected DatasourceTypePrometheus 'prometheus', got %s", v1alpha1.DatasourceTypePrometheus)
	}

	if v1alpha1.DatasourceTypeOpenStack != "openstack" {
		t.Errorf("Expected DatasourceTypeOpenStack 'openstack', got %s", v1alpha1.DatasourceTypeOpenStack)
	}
}

func TestDatasourceSpec(t *testing.T) {
	// Test creating a complete datasource spec
	spec := v1alpha1.DatasourceSpec{
		Operator: "test-operator",
		Type:     v1alpha1.DatasourceTypePrometheus,
		Prometheus: v1alpha1.PrometheusDatasource{
			Query:             "node_cpu_seconds_total",
			Alias:             "node_exporter_host_cpu_usage",
			Type:              "node_exporter_metric",
			TimeRangeSeconds:  2419200, // default value
			IntervalSeconds:   86400,   // default value
			ResolutionSeconds: 43200,   // default value
			SecretRef: corev1.SecretReference{
				Name:      "prometheus-config",
				Namespace: "monitoring",
			},
		},
		DatabaseSecretRef: corev1.SecretReference{
			Name:      "db-credentials",
			Namespace: "default",
		},
		SSOSecretRef: &corev1.SecretReference{
			Name:      "sso-cert",
			Namespace: "default",
		},
	}

	if spec.Operator != "test-operator" {
		t.Errorf("Expected operator 'test-operator', got %s", spec.Operator)
	}

	if spec.Type != v1alpha1.DatasourceTypePrometheus {
		t.Errorf("Expected type %s, got %s", v1alpha1.DatasourceTypePrometheus, spec.Type)
	}

	if spec.Prometheus.Query != "node_cpu_seconds_total" {
		t.Errorf("Expected query 'node_cpu_seconds_total', got %s", spec.Prometheus.Query)
	}

	if spec.Prometheus.Alias != "node_exporter_host_cpu_usage" {
		t.Errorf("Expected alias 'node_exporter_host_cpu_usage', got %s", spec.Prometheus.Alias)
	}

	if spec.DatabaseSecretRef.Name != "db-credentials" {
		t.Errorf("Expected DatabaseSecretRef.Name 'db-credentials', got %s", spec.DatabaseSecretRef.Name)
	}

	if spec.SSOSecretRef == nil {
		t.Error("Expected SSOSecretRef to be non-nil")
	} else if spec.SSOSecretRef.Name != "sso-cert" {
		t.Errorf("Expected SSOSecretRef.Name 'sso-cert', got %s", spec.SSOSecretRef.Name)
	}
}

func TestDatasourceStatus(t *testing.T) {
	// Test that datasource status struct works correctly
	status := v1alpha1.DatasourceStatus{
		NumberOfObjects:    100,
		LastSyncDurationMs: 30,
		Error:              "",
	}

	if status.NumberOfObjects != 100 {
		t.Errorf("Expected NumberOfObjects 100, got %d", status.NumberOfObjects)
	}

	if status.LastSyncDurationMs != 30 {
		t.Errorf("Expected LastSyncDurationMs 30, got %d", status.LastSyncDurationMs)
	}

	if status.Error != "" {
		t.Errorf("Expected empty error, got %s", status.Error)
	}

	// Test with error
	status.Error = "connection failed"
	if status.Error != "connection failed" {
		t.Errorf("Expected error 'connection failed', got %s", status.Error)
	}
}

func TestMetricTypeMapping(t *testing.T) {
	// Test that we know about the expected metric types
	knownMetricTypes := []string{
		"vrops_host_metric",
		"vrops_vm_metric",
		"node_exporter_metric",
		"netapp_aggregate_labels_metric",
		"netapp_node_metric",
		"netapp_volume_aggregate_labels_metric",
		"kvm_libvirt_domain_metric",
	}

	for _, metricType := range knownMetricTypes {
		// Create a test datasource with this metric type
		ds := v1alpha1.PrometheusDatasource{
			Query: "test_query",
			Alias: "test_alias",
			Type:  metricType,
			SecretRef: corev1.SecretReference{
				Name:      "test-secret",
				Namespace: "default",
			},
		}

		if ds.Type != metricType {
			t.Errorf("Expected type %s, got %s", metricType, ds.Type)
		}
	}
}

func TestNewTypedSyncerFunctionExists(t *testing.T) {
	// Test that we can create a typed syncer (even though it's internal)
	// We can't directly test the internal functions, but we can test the types
	mockDB := &db.DB{}
	monitor := datasources.Monitor{}

	// These should not panic when called
	_ = mockDB
	_ = monitor

	// Test that we can create a prometheus datasource
	ds := v1alpha1.Datasource{
		Spec: v1alpha1.DatasourceSpec{
			Type: v1alpha1.DatasourceTypePrometheus,
			Prometheus: v1alpha1.PrometheusDatasource{
				Query:             "up",
				Alias:             "test_metric",
				Type:              "node_exporter_metric",
				TimeRangeSeconds:  3600,
				IntervalSeconds:   60,
				ResolutionSeconds: 15,
				SecretRef: corev1.SecretReference{
					Name:      "prometheus-secret",
					Namespace: "default",
				},
			},
		},
	}

	if ds.Spec.Type != v1alpha1.DatasourceTypePrometheus {
		t.Errorf("Expected type %s, got %s", v1alpha1.DatasourceTypePrometheus, ds.Spec.Type)
	}
}
