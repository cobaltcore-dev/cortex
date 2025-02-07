// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"os"
	"testing"

	"gopkg.in/yaml.v2"
)

func createTempConfigFile(t *testing.T, content string) string {
	tmpDir := t.TempDir()
	tmpfile, err := os.CreateTemp(tmpDir, "yaml")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	return tmpfile.Name()
}

func TestNewConfig(t *testing.T) {
	content := `
sync:
  prometheus:
    metrics:
      - name: vrops_virtualmachine_cpu_demand_ratio
        type: vrops_vm_metric
        timeRangeSeconds: 2419200
        intervalSeconds: 86400
        resolutionSeconds: 43200
      - name: vrops_hostsystem_cpu_contention_percentage
        type: vrops_host_metric
  openstack:
    hypervisors: true
    servers: true
features:
  extractors:
    - name: vrops_hostsystem_resolver
    - name: vrops_project_noisiness_extractor
    - name: vrops_hostsystem_contention_extractor
scheduler:
  steps:
    - name: vrops_anti_affinity_noisy_projects
      options:
        avgCPUThreshold: 20
    - name: vrops_avoid_contended_hosts
      options:
        maxCPUContentionThreshold: 50
`
	filepath := createTempConfigFile(t, content)

	config := newConfigFromFile(filepath)

	// Test SyncConfig
	syncConfig := config.GetSyncConfig()
	if len(syncConfig.Prometheus.Metrics) != 2 {
		t.Errorf("Expected 2 Prometheus metrics, got %d", len(syncConfig.Prometheus.Metrics))
	}
	if !*syncConfig.OpenStack.HypervisorsEnabled {
		t.Errorf("Expected OpenStack hypervisors to be enabled")
	}
	if !*syncConfig.OpenStack.ServersEnabled {
		t.Errorf("Expected OpenStack servers to be enabled")
	}

	// Test FeaturesConfig
	featuresConfig := config.GetFeaturesConfig()
	if len(featuresConfig.Extractors) != 3 {
		t.Errorf("Expected 3 extractors, got %d", len(featuresConfig.Extractors))
	}

	// Test SchedulerConfig
	schedulerConfig := config.GetSchedulerConfig()
	if len(schedulerConfig.Steps) != 2 {
		t.Errorf("Expected 2 scheduler steps, got %d", len(schedulerConfig.Steps))
	}
	var decodedContent map[string]any
	if err := yaml.Unmarshal([]byte(content), &decodedContent); err != nil {
		t.Fatalf("Failed to unmarshal YAML content: %v", err)
	}

	schedulerSteps := decodedContent["scheduler"].(map[any]any)["steps"].([]any)
	step1Options := schedulerSteps[0].(map[any]any)["options"].(map[any]any)
	step2Options := schedulerSteps[1].(map[any]any)["options"].(map[any]any)

	if step1Options["avgCPUThreshold"] != 20 {
		t.Errorf("Expected avgCPUThreshold to be 20, got %v", step1Options["avgCPUThreshold"])
	}
	if step2Options["maxCPUContentionThreshold"] != 50 {
		t.Errorf("Expected maxCPUContentionThreshold to be 50, got %v", step2Options["maxCPUContentionThreshold"])
	}
}
