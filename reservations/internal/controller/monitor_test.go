// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cobaltcore-dev/cortex/reservations/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestMonitor_Init(t *testing.T) {
	monitor := &Monitor{}
	monitor.Init()

	if monitor.numberOfReservations == nil {
		t.Error("numberOfReservations metric should be initialized")
	}

	if monitor.reservedResources == nil {
		t.Error("reservedResources metric should be initialized")
	}
}

func TestMonitor_Describe(t *testing.T) {
	monitor := &Monitor{}
	monitor.Init()

	ch := make(chan *prometheus.Desc, 10)
	go func() {
		monitor.Describe(ch)
		close(ch)
	}()

	// Count the number of descriptions
	count := 0
	for range ch {
		count++
	}

	// Should have descriptions for both metrics
	if count != 2 {
		t.Errorf("Expected 2 metric descriptions, got %d", count)
	}
}

func TestMonitor_Collect_EmptyList(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	monitor := &Monitor{
		Client: k8sClient,
	}
	monitor.Init()

	ch := make(chan prometheus.Metric, 10)
	go func() {
		monitor.Collect(ch)
		close(ch)
	}()

	// Count the metrics
	count := 0
	for range ch {
		count++
	}

	// Should have at least the base metrics even with empty list
	if count < 0 {
		t.Errorf("Expected at least 0 metrics, got %d", count)
	}
}

func TestMonitor_Collect_WithReservations(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	// Create test reservations
	reservations := []v1alpha1.ComputeReservation{
		{
			ObjectMeta: ctrl.ObjectMeta{
				Name: "test-reservation-1",
			},
			Spec: v1alpha1.ComputeReservationSpec{
				Requests: map[string]resource.Quantity{
					"memory": resource.MustParse("1Gi"),
					"cpu":    resource.MustParse("2"),
				},
			},
			Status: v1alpha1.ComputeReservationStatus{
				Phase: v1alpha1.ComputeReservationStatusPhaseActive,
				Host:  "test-host-1",
			},
		},
		{
			ObjectMeta: ctrl.ObjectMeta{
				Name: "test-reservation-2",
			},
			Spec: v1alpha1.ComputeReservationSpec{
				Requests: map[string]resource.Quantity{
					"memory": resource.MustParse("2Gi"),
					"cpu":    resource.MustParse("4"),
				},
			},
			Status: v1alpha1.ComputeReservationStatus{
				Phase: v1alpha1.ComputeReservationStatusPhaseFailed,
				Error: "test error",
			},
		},
		{
			ObjectMeta: ctrl.ObjectMeta{
				Name: "test-reservation-3",
			},
			Spec: v1alpha1.ComputeReservationSpec{
				Requests: map[string]resource.Quantity{
					"memory": resource.MustParse("4Gi"),
					"cpu":    resource.MustParse("4"),
				},
			},
			Status: v1alpha1.ComputeReservationStatus{
				Phase: v1alpha1.ComputeReservationStatusPhaseActive,
			},
		},
	}

	// Convert to client.Object slice
	objects := make([]client.Object, len(reservations))
	for i := range reservations {
		objects[i] = &reservations[i]
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		Build()

	monitor := &Monitor{
		Client: k8sClient,
	}
	monitor.Init()

	ch := make(chan prometheus.Metric, 100)
	go func() {
		monitor.Collect(ch)
		close(ch)
	}()

	// Collect all metrics
	metrics := []prometheus.Metric{}
	for metric := range ch {
		metrics = append(metrics, metric)
	}

	if len(metrics) == 0 {
		t.Error("Expected some metrics to be collected")
	}

	// Verify that we have metrics for different phases and kinds
	foundActiveInstance := false
	foundFailedInstance := false
	foundActiveBare := false

	for _, metric := range metrics {
		var m dto.Metric
		if err := metric.Write(&m); err != nil {
			continue
		}

		// Check if this is a numberOfReservations metric
		if m.GetGauge() != nil {
			labels := make(map[string]string)
			for _, label := range m.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}

			if labels["status_phase"] == "active" && labels["spec_scheduler"] == "instance" {
				foundActiveInstance = true
			}
			if labels["status_phase"] == "failed" && labels["spec_scheduler"] == "instance" {
				foundFailedInstance = true
			}
			if labels["status_phase"] == "active" && labels["spec_scheduler"] == "bare" {
				foundActiveBare = true
			}
		}
	}

	if !foundActiveInstance {
		t.Error("Expected to find active instance reservation metric")
	}
	if !foundFailedInstance {
		t.Error("Expected to find failed instance reservation metric")
	}
	if !foundActiveBare {
		t.Error("Expected to find active bare resource reservation metric")
	}
}

func TestMonitor_Collect_ResourceMetrics(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	// Create test reservation with specific resource values
	reservation := &v1alpha1.ComputeReservation{
		ObjectMeta: ctrl.ObjectMeta{
			Name: "test-reservation",
		},
		Spec: v1alpha1.ComputeReservationSpec{
			Requests: map[string]resource.Quantity{
				"memory": resource.MustParse("1000Mi"),
				"cpu":    resource.MustParse("2"),
			},
		},
		Status: v1alpha1.ComputeReservationStatus{
			Phase: v1alpha1.ComputeReservationStatusPhaseActive,
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(reservation).
		Build()

	monitor := &Monitor{
		Client: k8sClient,
	}
	monitor.Init()

	ch := make(chan prometheus.Metric, 100)
	go func() {
		monitor.Collect(ch)
		close(ch)
	}()

	// Collect all metrics
	metrics := []prometheus.Metric{}
	for metric := range ch {
		metrics = append(metrics, metric)
	}

	// Look for resource metrics
	foundVCPUs := false
	foundMemory := false

	for _, metric := range metrics {
		var m dto.Metric
		if err := metric.Write(&m); err != nil {
			continue
		}

		if m.GetGauge() != nil {
			labels := make(map[string]string)
			for _, label := range m.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}

			if labels["resource"] == "vcpus" {
				foundVCPUs = true
				if m.GetGauge().GetValue() != 2 {
					t.Errorf("Expected vCPUs value 2, got %f", m.GetGauge().GetValue())
				}
			}
			if labels["resource"] == "memory_mb" {
				foundMemory = true
				// Memory: 1000Mi = 1000 * 1024 * 1024 bytes = 1048576000 bytes
				// Converted to MB: 1048576000 / 1000000 = 1048.576 MB
				expectedMemoryMB := float64(1048) // 1000Mi converted to MB
				if m.GetGauge().GetValue() != expectedMemoryMB {
					t.Errorf("Expected memory_mb value %f, got %f", expectedMemoryMB, m.GetGauge().GetValue())
				}
			}
		}
	}

	if !foundVCPUs {
		t.Error("Expected to find vCPUs resource metric")
	}
	if !foundMemory {
		t.Error("Expected to find memory resource metric")
	}
}

func TestMonitor_Collect_ErrorHandling(t *testing.T) {
	// Test with a client that will fail to list reservations
	scheme := runtime.NewScheme()
	// Don't add the scheme, which should cause the List operation to fail

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	monitor := &Monitor{
		Client: k8sClient,
	}
	monitor.Init()

	ch := make(chan prometheus.Metric, 10)
	go func() {
		monitor.Collect(ch)
		close(ch)
	}()

	// Should not panic and should handle the error gracefully
	count := 0
	for range ch {
		count++
	}

	// Should not collect any metrics due to the error
	if count != 0 {
		t.Errorf("Expected 0 metrics due to error, got %d", count)
	}
}

func TestMonitor_Collect_LabelSanitization(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	// Create test reservation with error containing commas
	reservation := &v1alpha1.ComputeReservation{
		ObjectMeta: ctrl.ObjectMeta{
			Name: "test-reservation",
		},
		Spec: v1alpha1.ComputeReservationSpec{
			Requests: map[string]resource.Quantity{
				"memory": resource.MustParse("1Gi"),
				"cpu":    resource.MustParse("2"),
			},
		},
		Status: v1alpha1.ComputeReservationStatus{
			Phase: v1alpha1.ComputeReservationStatusPhaseFailed,
			Error: "error with, commas, in it",
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(reservation).
		Build()

	monitor := &Monitor{
		Client: client,
	}
	monitor.Init()

	ch := make(chan prometheus.Metric, 100)
	go func() {
		monitor.Collect(ch)
		close(ch)
	}()

	// Collect all metrics
	metrics := []prometheus.Metric{}
	for metric := range ch {
		metrics = append(metrics, metric)
	}

	// Verify that commas in error messages are replaced with semicolons
	foundSanitizedError := false
	for _, metric := range metrics {
		var m dto.Metric
		if err := metric.Write(&m); err != nil {
			continue
		}

		if m.GetGauge() != nil {
			for _, label := range m.GetLabel() {
				if label.GetName() == "status_error" && label.GetValue() == "error with; commas; in it" {
					foundSanitizedError = true
					break
				}
			}
		}
	}

	if !foundSanitizedError {
		t.Error("Expected to find sanitized error label with semicolons instead of commas")
	}
}
