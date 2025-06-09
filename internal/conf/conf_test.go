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
          "name": "vrops_hostsystem_cpu_contention_long_term_percentage",
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
  "extractor": {
    "plugins": [
      {
        "name": "vrops_hostsystem_resolver"
      },
      {
        "name": "vrops_project_noisiness_extractor"
      },
      {
        "name": "vrops_hostsystem_contention_long_term_extractor"
      }
    ]
  },
  "scheduler": {
    "nova": {
      "plugins": [
        {
            "name": "vmware_anti_affinity_noisy_projects",
            "options": {
            "avgCPUThreshold": 20
            }
        },
        {
            "name": "vmware_avoid_long_term_contended_hosts",
            "options": {
            "maxCPUContentionThreshold": 50
            }
        }
      ]
    }
  },
  "kpis": {
    "plugins": [
      {
        "name": "vm_life_span_kpi"
      }
    ]
  },
  "logging": {
    "level": "debug",
    "format": "text"
  },
  "db": {
    "primary": {
      "host": "cortex-postgresql-primary",
      "port": 5432,
      "user": "postgres",
      "password": "secret",
      "database": "postgres"
    },
	"readonly": {
      "host": "cortex-postgresql-readonly",
      "port": 5432,
      "user": "postgres",
      "password": "secret",
      "database": "postgres"
    },
},
  "monitoring": {
    "port": 2112,
    "labels": {
      "github_org": "cobaltcore-dev",
      "github_repo": "cortex"
    }
  },
  "mqtt": {
    "url": "tcp://cortex-mqtt:1883",
    "username": "cortex",
    "password": "secret"
  },
  "api": {
    "port": 8080
  }
}`
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

	// Test ExtractorConfig
	extractorConfig := config.GetExtractorConfig()
	if len(extractorConfig.Plugins) != 3 {
		t.Errorf("Expected 3 extractors, got %d", len(extractorConfig.Plugins))
	}

	// Test SchedulerConfig
	schedulerConfig := config.GetSchedulerConfig()
	if len(schedulerConfig.Nova.Plugins) != 2 {
		t.Errorf("Expected 2 scheduler steps, got %d", len(schedulerConfig.Nova.Plugins))
	}
	var decodedContent map[string]any
	if err := json.Unmarshal([]byte(content), &decodedContent); err != nil {
		t.Fatalf("Failed to unmarshal YAML content: %v", err)
	}

	schedulerSteps := decodedContent["scheduler"].(map[string]any)["nova"].(map[string]any)["plugins"].([]any)
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

	// Test LoggingConfig
	loggingConfig := config.GetLoggingConfig()
	if loggingConfig.LevelStr == "" {
		t.Errorf("Expected non-empty log level, got empty string")
	}
	if loggingConfig.Format == "" {
		t.Errorf("Expected non-empty log format, got empty string")
	}

	// Test DBConfig
	dbConfig := config.GetDBConfig()
	if dbConfig.Primary.Host == "" {
		t.Errorf("Expected non-empty DB host, got empty string")
	}
	if dbConfig.Primary.Port == 0 {
		t.Errorf("Expected non-zero DB port, got 0")
	}
	if dbConfig.Primary.Database == "" {
		t.Errorf("Expected non-empty DB name, got empty string")
	}
	if dbConfig.Primary.User == "" {
		t.Errorf("Expected non-empty DB user, got empty string")
	}
	if dbConfig.Primary.Password == "" {
		t.Errorf("Expected non-empty DB password, got empty string")
	}
	// Check the read-only DB configuration as well
	if dbConfig.ReadOnly.Host == "" {
		t.Errorf("Expected non-empty read-only DB host, got empty string")
	}
	if dbConfig.ReadOnly.Port == 0 {
		t.Errorf("Expected non-zero read-only DB port, got 0")
	}
	if dbConfig.ReadOnly.Database == "" {
		t.Errorf("Expected non-empty read-only DB name, got empty string")
	}
	if dbConfig.ReadOnly.User == "" {
		t.Errorf("Expected non-empty read-only DB user, got empty string")
	}
	if dbConfig.ReadOnly.Password == "" {
		t.Errorf("Expected non-empty read-only DB password, got empty string")
	}

	// Test MonitoringConfig
	monitoringConfig := config.GetMonitoringConfig()
	if len(monitoringConfig.Labels) == 0 {
		t.Errorf("Expected non-empty monitoring labels, got empty map")
	}
	if monitoringConfig.Port == 0 {
		t.Errorf("Expected non-zero monitoring port, got 0")
	}

	// Test MQTTConfig
	mqttConfig := config.GetMQTTConfig()
	if mqttConfig.URL == "" {
		t.Errorf("Expected non-empty MQTT URL, got empty string")
	}
	if mqttConfig.Username == "" {
		t.Errorf("Expected non-empty MQTT username, got empty string")
	}
	if mqttConfig.Password == "" {
		t.Errorf("Expected non-empty MQTT password, got empty string")
	}

	// Test APIConfig
	apiConfig := config.GetAPIConfig()
	if apiConfig.Port == 0 {
		t.Errorf("Expected non-zero API port, got 0")
	}
}
