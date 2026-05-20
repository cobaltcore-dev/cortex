// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

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
	monitor.requestCounter.WithLabelValues("200", "false", "accepted").Inc()
	monitor.requestDuration.WithLabelValues("200", "false").Observe(0.1)
	monitor.commitmentChanges.WithLabelValues("accepted", "az-1", "false").Inc()
	monitor.timeouts.WithLabelValues("false").Inc()

	// Verify metrics can be gathered
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}

	// Check that all metrics are present
	foundRequestCounter := false
	foundRequestDuration := false
	foundCommitmentChanges := false
	foundTimeouts := false

	for _, family := range families {
		switch *family.Name {
		case "cortex_committed_resource_change_api_requests_total":
			foundRequestCounter = true
			if *family.Type != dto.MetricType_COUNTER {
				t.Errorf("Expected counter metric type, got %v", *family.Type)
			}
		case "cortex_committed_resource_change_api_request_duration_seconds":
			foundRequestDuration = true
			if *family.Type != dto.MetricType_HISTOGRAM {
				t.Errorf("Expected histogram metric type, got %v", *family.Type)
			}
		case "cortex_committed_resource_change_api_commitment_changes_total":
			foundCommitmentChanges = true
			if *family.Type != dto.MetricType_COUNTER {
				t.Errorf("Expected counter metric type, got %v", *family.Type)
			}
		case "cortex_committed_resource_change_api_timeouts_total":
			foundTimeouts = true
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
	if !foundTimeouts {
		t.Error("Timeouts counter not found in registry")
	}
}

func TestChangeCommitmentsAPIMonitor_MetricLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	monitor := NewChangeCommitmentsAPIMonitor()

	if err := registry.Register(&monitor); err != nil {
		t.Fatalf("Failed to register monitor: %v", err)
	}

	// Record some test metrics
	monitor.requestCounter.WithLabelValues("200", "false", "accepted").Inc()
	monitor.requestCounter.WithLabelValues("409", "false", "error").Inc()
	monitor.requestCounter.WithLabelValues("503", "false", "error").Inc()
	monitor.requestDuration.WithLabelValues("200", "false").Observe(1.5)
	monitor.commitmentChanges.WithLabelValues("accepted", "az-1", "false").Add(5)
	monitor.commitmentChanges.WithLabelValues("rejected", "az-1", "false").Add(2)

	// Gather metrics
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}

	// Verify request counter has correct labels
	for _, family := range families {
		if *family.Name == "cortex_committed_resource_change_api_requests_total" {
			// At minimum we expect the 3 labels we added (200, 409, 503)
			// Plus pre-initialized labels (400, 500) - so >= 5 total
			if len(family.Metric) < 3 {
				t.Errorf("Expected at least 3 request counter metrics, got %d", len(family.Metric))
			}

			// Check all metrics have the status_code, dry_run, and result labels
			for _, metric := range family.Metric {
				labelNames := make(map[string]bool)
				for _, label := range metric.Label {
					labelNames[*label.Name] = true
				}

				if !labelNames["status_code"] {
					t.Error("Missing 'status_code' label in request counter")
				}
				if !labelNames["dry_run"] {
					t.Error("Missing 'dry_run' label in request counter")
				}
				if !labelNames["result"] {
					t.Error("Missing 'result' label in request counter")
				}
			}
		}

		if *family.Name == "cortex_committed_resource_change_api_request_duration_seconds" {
			// At minimum we expect the label we used (200)
			// Plus pre-initialized labels - so >= 1 total
			if len(family.Metric) < 1 {
				t.Errorf("Expected at least 1 histogram metric, got %d", len(family.Metric))
			}

			// Check all metrics have the status_code and dry_run labels
			for _, metric := range family.Metric {
				labelNames := make(map[string]bool)
				for _, label := range metric.Label {
					labelNames[*label.Name] = true
				}

				if !labelNames["status_code"] {
					t.Error("Missing 'status_code' label in histogram")
				}
				if !labelNames["dry_run"] {
					t.Error("Missing 'dry_run' label in histogram")
				}
			}
		}

		if *family.Name == "cortex_committed_resource_change_api_commitment_changes_total" {
			// 2 label combinations: (accepted,az-1,false) and (rejected,az-1,false)
			if len(family.Metric) < 2 {
				t.Errorf("Expected at least 2 commitment changes metrics, got %d", len(family.Metric))
			}

			// Check all metrics have result, az, and dry_run labels
			for _, metric := range family.Metric {
				labelNames := make(map[string]bool)
				for _, label := range metric.Label {
					labelNames[*label.Name] = true
				}

				if !labelNames["result"] {
					t.Error("Missing 'result' label in commitment changes counter")
				}
				if !labelNames["az"] {
					t.Error("Missing 'az' label in commitment changes counter")
				}
				if !labelNames["dry_run"] {
					t.Error("Missing 'dry_run' label in commitment changes counter")
				}
			}
		}
	}
}

func TestComputeNetUnitDeltas(t *testing.T) {
	testCases := []struct {
		name     string
		request  CommitmentChangeRequest
		expected map[string]int64 // resourceName → expected net delta
	}{
		{
			name: "single project increase",
			request: newCommitmentRequest("az-a", true, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-1", "confirmed", 5)),
			expected: map[string]int64{"hw_version_hana_1_ram": 5},
		},
		{
			name: "single project decrease",
			request: newCommitmentRequest("az-a", true, 1234,
				deleteCommitment("hw_version_hana_1_ram", "project-A", "uuid-1", "confirmed", 5)),
			expected: map[string]int64{"hw_version_hana_1_ram": -5},
		},
		{
			name: "two projects same resource: +5 and -5 cancel to zero",
			request: newCommitmentRequest("az-a", true, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-a", "confirmed", 5),
				deleteCommitment("hw_version_hana_1_ram", "project-B", "uuid-b", "confirmed", 5)),
			expected: map[string]int64{"hw_version_hana_1_ram": 0},
		},
		{
			name: "two projects same resource: +5 and -3 leaves net +2",
			request: newCommitmentRequest("az-a", true, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-a", "confirmed", 5),
				deleteCommitment("hw_version_hana_1_ram", "project-B", "uuid-b", "confirmed", 3)),
			expected: map[string]int64{"hw_version_hana_1_ram": 2},
		},
		{
			name: "two projects same resource: +3 and -5 leaves net -2",
			request: newCommitmentRequest("az-a", true, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-a", "confirmed", 3),
				deleteCommitment("hw_version_hana_1_ram", "project-B", "uuid-b", "confirmed", 5)),
			expected: map[string]int64{"hw_version_hana_1_ram": -2},
		},
		{
			name: "two resources tracked independently",
			request: newCommitmentRequest("az-a", true, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-1", "confirmed", 4),
				createCommitment("hw_version_hana_2_ram", "project-A", "uuid-2", "confirmed", 2)),
			expected: map[string]int64{
				"hw_version_hana_1_ram": 4,
				"hw_version_hana_2_ram": 2,
			},
		},
		{
			name: "two resources: one net positive, one net zero",
			request: newCommitmentRequest("az-a", true, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-a1", "confirmed", 5),
				deleteCommitment("hw_version_hana_1_ram", "project-B", "uuid-b1", "confirmed", 5),
				createCommitment("hw_version_hana_2_ram", "project-A", "uuid-a2", "confirmed", 3)),
			expected: map[string]int64{
				"hw_version_hana_1_ram": 0,
				"hw_version_hana_2_ram": 3,
			},
		},
		{
			name:     "empty request",
			request:  newCommitmentRequest("az-a", true, 1234),
			expected: map[string]int64{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			reqJSON := buildRequestJSON(tc.request)
			var req liquid.CommitmentChangeRequest
			if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
				t.Fatalf("parse request: %v", err)
			}

			got := computeNetUnitDeltas(req)

			if len(got) != len(tc.expected) {
				t.Fatalf("expected %d resource entries, got %d: %v", len(tc.expected), len(got), got)
			}
			for name, want := range tc.expected {
				if got[liquid.ResourceName(name)] != want {
					t.Errorf("resource %q: want delta %d, got %d", name, want, got[liquid.ResourceName(name)])
				}
			}
		})
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
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-1", "confirmed", 2),
			),
			expected: 1,
		},
		{
			name: "Multiple commitments same project",
			request: newCommitmentRequest("az-a", false, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-1", "confirmed", 2),
				createCommitment("hw_version_hana_2_ram", "project-A", "uuid-2", "confirmed", 2),
			),
			expected: 2,
		},
		{
			name: "Multiple commitments multiple projects",
			request: newCommitmentRequest("az-a", false, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-1", "confirmed", 2),
				createCommitment("hw_version_hana_1_ram", "project-B", "uuid-2", "confirmed", 3),
				createCommitment("hw_version_gp_1_ram", "project-C", "uuid-3", "confirmed", 1),
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
