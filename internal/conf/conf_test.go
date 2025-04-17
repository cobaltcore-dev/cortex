// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"encoding/json"
	"os"
	"testing"
)

func createTempConfigFile(t *testing.T, content string) string {
	tmpDir := t.TempDir()
	tmpfile, err := os.CreateTemp(tmpDir, "json")
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
{
  "sync": {
    "prometheus": {
      "metrics": [
        {
          "name": "vrops_virtualmachine_cpu_demand_ratio",
          "type": "vrops_vm_metric",
          "timeRangeSeconds": 2419200,
          "intervalSeconds": 86400,
          "resolutionSeconds": 43200
        },
        {
          "name": "vrops_hostsystem_cpu_contention_percentage",
          "type": "vrops_host_metric"
        }
      ]
    },
    "openstack": {
      "nova": {
        "types": [
          "server",
          "hypervisor"
        ]
      }
    }
  },
  "features": {
    "plugins": [
      {
        "name": "vrops_hostsystem_resolver"
      },
      {
        "name": "vrops_project_noisiness_extractor"
      },
      {
        "name": "vrops_hostsystem_contention_extractor"
      }
    ]
  },
  "scheduler": {
    "plugins": [
      {
        "name": "vmware_anti_affinity_noisy_projects",
        "options": {
          "avgCPUThreshold": 20
        }
      },
      {
        "name": "vmware_avoid_contended_hosts",
        "options": {
          "maxCPUContentionThreshold": 50
        }
      }
    ]
  },
  "kpis": {
	"plugins": [
	  {
	    "name": "vm_life_span_kpi"
	  }
    ]
  }
}
`
	filepath := createTempConfigFile(t, content)

	config := newConfigFromFile(filepath)

	// Test SyncConfig
	syncConfig := config.GetSyncConfig()
	if len(syncConfig.Prometheus.Metrics) != 2 {
		t.Errorf("Expected 2 Prometheus metrics, got %d", len(syncConfig.Prometheus.Metrics))
	}
	if len(syncConfig.OpenStack.Nova.Types) != 2 {
		t.Errorf("Expected 2 OpenStack types, got %d", len(syncConfig.OpenStack.Nova.Types))
	}

	// Test FeaturesConfig
	featuresConfig := config.GetFeaturesConfig()
	if len(featuresConfig.Plugins) != 3 {
		t.Errorf("Expected 3 extractors, got %d", len(featuresConfig.Plugins))
	}

	// Test SchedulerConfig
	schedulerConfig := config.GetSchedulerConfig()
	if len(schedulerConfig.Plugins) != 2 {
		t.Errorf("Expected 2 scheduler steps, got %d", len(schedulerConfig.Plugins))
	}
	var decodedContent map[string]any
	if err := json.Unmarshal([]byte(content), &decodedContent); err != nil {
		t.Fatalf("Failed to unmarshal YAML content: %v", err)
	}

	schedulerSteps := decodedContent["scheduler"].(map[string]any)["plugins"].([]any)
	step1Options := schedulerSteps[0].(map[string]any)["options"].(map[string]any)
	step2Options := schedulerSteps[1].(map[string]any)["options"].(map[string]any)

	if step1Options["avgCPUThreshold"] != 20.0 {
		t.Errorf("Expected avgCPUThreshold to be 20, got %v", step1Options["avgCPUThreshold"])
	}
	if step2Options["maxCPUContentionThreshold"] != 50.0 {
		t.Errorf("Expected maxCPUContentionThreshold to be 50, got %v", step2Options["maxCPUContentionThreshold"])
	}

	// Test KPIsConfig
	kpisConfig := config.GetKPIsConfig()
	if len(kpisConfig.Plugins) != 1 {
		t.Errorf("Expected 1 kpi, got %d", len(kpisConfig.Plugins))
	}
}
