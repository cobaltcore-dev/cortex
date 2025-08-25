// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"testing"

	v1alpha1 "github.com/cobaltcore-dev/cortex/reservations/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// createTestMonitorScheme creates a runtime scheme with the v1alpha1.ComputeReservation type registered
func createTestMonitorScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()

	// Register the v1alpha1.ComputeReservation type
	gv := schema.GroupVersion{Group: "reservations.cortex", Version: "v1alpha1"}
	scheme.AddKnownTypes(gv, &v1alpha1.ComputeReservation{}, &v1alpha1.ComputeReservationList{})
	metav1.AddToGroupVersion(scheme, gv)

	return scheme
}

// createTestReservationForMonitor creates a test reservation with the given parameters
func createTestReservationForMonitor(name, phase, errorMsg string, vcpus, memoryMB, diskGB int64) *v1alpha1.ComputeReservation {
	reservation := &v1alpha1.ComputeReservation{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "reservations.cortex/v1alpha1",
			Kind:       "ComputeReservation",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "test-namespace",
		},
		Spec: v1alpha1.ComputeReservationSpec{
			Kind:      v1alpha1.ComputeReservationSpecKindInstance,
			ProjectID: "test-project",
			DomainID:  "test-domain",
			Instance: v1alpha1.ComputeReservationSpecInstance{
				Flavor: "test-flavor",
				Memory: *resource.NewQuantity(memoryMB*1024*1024, resource.BinarySI), // Convert MB to bytes
				VCPUs:  *resource.NewQuantity(vcpus, resource.DecimalSI),
				Disk:   *resource.NewQuantity(diskGB*1024*1024*1024, resource.BinarySI), // Convert GB to bytes
			},
		},
		Status: v1alpha1.ComputeReservationStatus{
			Phase: v1alpha1.ComputeReservationStatusPhase(phase),
			Error: errorMsg,
			Host:  "test-host",
		},
	}

	return reservation
}

func TestMonitor_Init(t *testing.T) {
	client := fake.NewClientBuilder().
		WithScheme(createTestMonitorScheme()).
		Build()

	monitor := &Monitor{Client: client}

	// Test initialization
	monitor.Init()

	// Verify that metrics are initialized
	if monitor.numberOfReservations == nil {
		t.Error("numberOfReservations metric should be initialized")
	}
	if monitor.reservedResources == nil {
		t.Error("reservedResources metric should be initialized")
	}
}

func TestMonitor_Collect(t *testing.T) {
	reservations := []*v1alpha1.ComputeReservation{
		createTestReservationForMonitor("reservation-1", "active", "", 2, 2048, 10),
		createTestReservationForMonitor("reservation-2", "failed", "scheduling failed", 4, 4096, 20),
	}

	var runtimeObjects []runtime.Object
	for _, res := range reservations {
		runtimeObjects = append(runtimeObjects, res)
	}

	client := fake.NewClientBuilder().
		WithScheme(createTestMonitorScheme()).
		WithRuntimeObjects(runtimeObjects...).
		Build()

	monitor := &Monitor{Client: client}

	monitor.Init()

	// Test collection
	metrics := make(chan prometheus.Metric, 100)
	monitor.Collect(metrics)
	close(metrics)

	// Collect all metrics
	var collectedMetrics []prometheus.Metric
	for metric := range metrics {
		collectedMetrics = append(collectedMetrics, metric)
	}

	// We expect metrics for reservation counts and resources
	if len(collectedMetrics) == 0 {
		t.Error("expected metrics to be collected")
	}

	// Verify we have both types of metrics
	foundReservationMetric := false
	foundResourceMetric := false

	for _, metric := range collectedMetrics {
		dto := &dto.Metric{}
		if err := metric.Write(dto); err != nil {
			t.Fatalf("failed to write metric: %v", err)
		}

		desc := metric.Desc()
		fqName := desc.String()

		switch fqName {
		case `Desc{fqName: "cortex_reservations_number", help: "Number of reservations.", constLabels: {}, variableLabels: {status_phase,status_error,spec_kind}}`:
			foundReservationMetric = true
			// Verify metric has proper labels
			labels := dto.GetLabel()
			if len(labels) != 3 {
				t.Errorf("expected 3 labels for reservations count, got %d", len(labels))
			}
		case `Desc{fqName: "cortex_reservations_resources", help: "Resources reserved by reservations.", constLabels: {}, variableLabels: {status_phase,status_error,spec_kind,allocation_kind,host,resource}}`:
			foundResourceMetric = true
			// Verify metric has proper labels
			labels := dto.GetLabel()
			if len(labels) != 6 {
				t.Errorf("expected 6 labels for resource metric, got %d", len(labels))
			}
		}
	}

	if !foundReservationMetric {
		t.Error("expected to find reservation count metric")
	}
	if !foundResourceMetric {
		t.Error("expected to find resource metric")
	}
}

func TestMonitor_Collect_EmptyReservations(t *testing.T) {
	client := fake.NewClientBuilder().
		WithScheme(createTestMonitorScheme()).
		Build()

	monitor := &Monitor{Client: client}

	monitor.Init()

	// Test collection with no reservations
	metrics := make(chan prometheus.Metric, 100)
	monitor.Collect(metrics)
	close(metrics)

	// Should have no metrics since there are no reservations
	metricCount := 0
	for range metrics {
		metricCount++
	}

	if metricCount != 0 {
		t.Errorf("expected 0 metrics with no reservations, got %d", metricCount)
	}
}
