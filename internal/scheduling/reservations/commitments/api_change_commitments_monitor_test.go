// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"encoding/json"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/sapcc/go-api-declarations/liquid"
)

func TestChangeCommitmentsAPIMonitor_MetricsRegistration(t *testing.T) {
	registry := prometheus.NewRegistry()
	monitor := NewChangeCommitmentsAPIMonitor()

	if err := registry.Register(&monitor); err != nil {
		t.Fatalf("Failed to register monitor: %v", err)
	}

	// Observe metrics before gathering (Prometheus metrics with labels only appear after being used)
	monitor.requestCounter.WithLabelValues("200").Inc()
	monitor.requestDuration.WithLabelValues("200").Observe(0.1)
	monitor.commitmentChanges.WithLabelValues("success").Inc()

	// Verify metrics can be gathered
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}

	// Check that all three metrics are present
	foundRequestCounter := false
	foundRequestDuration := false
	foundCommitmentChanges := false

	for _, family := range families {
		switch *family.Name {
		case "cortex_cr_change_api_requests_total":
			foundRequestCounter = true
			if *family.Type != dto.MetricType_COUNTER {
				t.Errorf("Expected counter metric type, got %v", *family.Type)
			}
		case "cortex_cr_change_api_request_duration_seconds":
			foundRequestDuration = true
			if *family.Type != dto.MetricType_HISTOGRAM {
				t.Errorf("Expected histogram metric type, got %v", *family.Type)
			}
		case "cortex_cr_change_api_commitment_changes_total":
			foundCommitmentChanges = true
			if *family.Type != dto.MetricType_COUNTER {
				t.Errorf("Expected counter metric type, got %v", *family.Type)
			}
		}
	}

	if !foundRequestCounter {
		t.Error("Request counter metric not found in registry")
	}
	if !foundRequestDuration {
		t.Error("Request duration histogram not found in registry")
	}
	if !foundCommitmentChanges {
		t.Error("Commitment changes counter not found in registry")
	}
}

func TestChangeCommitmentsAPIMonitor_MetricLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	monitor := NewChangeCommitmentsAPIMonitor()

	if err := registry.Register(&monitor); err != nil {
		t.Fatalf("Failed to register monitor: %v", err)
	}

	// Record some test metrics
	monitor.requestCounter.WithLabelValues("200").Inc()
	monitor.requestCounter.WithLabelValues("409").Inc()
	monitor.requestCounter.WithLabelValues("503").Inc()
	monitor.requestDuration.WithLabelValues("200").Observe(1.5)
	monitor.commitmentChanges.WithLabelValues("success").Add(5)
	monitor.commitmentChanges.WithLabelValues("rejected").Add(2)

	// Gather metrics
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}

	// Verify request counter has correct labels
	for _, family := range families {
		if *family.Name == "cortex_cr_change_api_requests_total" {
			if len(family.Metric) != 3 {
				t.Errorf("Expected 3 request counter metrics, got %d", len(family.Metric))
			}

			// Check label names
			for _, metric := range family.Metric {
				labelNames := make(map[string]bool)
				for _, label := range metric.Label {
					labelNames[*label.Name] = true
				}

				if !labelNames["status_code"] {
					t.Error("Missing 'status_code' label in request counter")
				}
			}
		}

		if *family.Name == "cortex_cr_change_api_request_duration_seconds" {
			if len(family.Metric) != 1 {
				t.Errorf("Expected 1 histogram metric, got %d", len(family.Metric))
			}

			// Check label names
			for _, metric := range family.Metric {
				labelNames := make(map[string]bool)
				for _, label := range metric.Label {
					labelNames[*label.Name] = true
				}

				if !labelNames["status_code"] {
					t.Error("Missing 'status_code' label in histogram")
				}
			}
		}

		if *family.Name == "cortex_cr_change_api_commitment_changes_total" {
			if len(family.Metric) != 2 {
				t.Errorf("Expected 2 commitment changes metrics, got %d", len(family.Metric))
			}

			// Check label names
			for _, metric := range family.Metric {
				labelNames := make(map[string]bool)
				for _, label := range metric.Label {
					labelNames[*label.Name] = true
				}

				if !labelNames["result"] {
					t.Error("Missing 'result' label in commitment changes counter")
				}
			}
		}
	}
}

func TestCountCommitments(t *testing.T) {
	testCases := []struct {
		name     string
		request  CommitmentChangeRequest
		expected int
	}{
		{
			name: "Single commitment",
			request: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid-1", "confirmed", 2),
			),
			expected: 1,
		},
		{
			name: "Multiple commitments same project",
			request: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid-1", "confirmed", 2),
				createCommitment("ram_hana_2", "project-A", "uuid-2", "confirmed", 2),
			),
			expected: 2,
		},
		{
			name: "Multiple commitments multiple projects",
			request: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid-1", "confirmed", 2),
				createCommitment("ram_hana_1", "project-B", "uuid-2", "confirmed", 3),
				createCommitment("ram_gp_1", "project-C", "uuid-3", "confirmed", 1),
			),
			expected: 3,
		},
		{
			name:     "Empty request",
			request:  newCommitmentRequest("az-a", false, 1234),
			expected: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Convert test request to liquid request
			reqJSON := buildRequestJSON(tc.request)
			var req liquid.CommitmentChangeRequest
			if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
				t.Fatalf("Failed to parse request: %v", err)
			}

			result := countCommitments(req)

			if result != tc.expected {
				t.Errorf("Expected %d commitments, got %d", tc.expected, result)
			}
		})
	}
}
