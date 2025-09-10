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
		  "alias": "my_custom_metric",
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
      "pipelines": [{
        "name": "default",
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
      }]
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
    "host": "cortex-postgresql",
    "port": 5432,
    "user": "postgres",
    "password": "secret",
    "database": "postgres"
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

	rawConfig, err := readRawConfig(filepath)
	if err != nil {
		t.Fatalf("Failed to read config: %v", err)
	}
	config := newConfigFromMaps[*SharedConfig](rawConfig, nil)

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
	if len(schedulerConfig.Nova.Pipelines) != 1 {
		t.Errorf("Expected 1 Nova scheduler pipeline, got %d", len(schedulerConfig.Nova.Pipelines))
	}
	if len(schedulerConfig.Nova.Pipelines[0].Plugins) != 2 {
		t.Errorf("Expected 2 scheduler steps, got %d", len(schedulerConfig.Nova.Pipelines[0].Plugins))
	}
	var decodedContent map[string]any
	if err := json.Unmarshal([]byte(content), &decodedContent); err != nil {
		t.Fatalf("Failed to unmarshal YAML content: %v", err)
	}

	schedulerPipelines := decodedContent["scheduler"].(map[string]any)["nova"].(map[string]any)["pipelines"].([]any)
	step1Options := schedulerPipelines[0].(map[string]any)["plugins"].([]any)[0].(map[string]any)["options"].(map[string]any)
	step2Options := schedulerPipelines[0].(map[string]any)["plugins"].([]any)[1].(map[string]any)["options"].(map[string]any)

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
	if dbConfig.Host == "" {
		t.Errorf("Expected non-empty DB host, got empty string")
	}
	if dbConfig.Port == 0 {
		t.Errorf("Expected non-zero DB port, got 0")
	}
	if dbConfig.Database == "" {
		t.Errorf("Expected non-empty DB name, got empty string")
	}
	if dbConfig.User == "" {
		t.Errorf("Expected non-empty DB user, got empty string")
	}
	if dbConfig.Password == "" {
		t.Errorf("Expected non-empty DB password, got empty string")
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

func TestMergeMaps(t *testing.T) {
	// Test basic merge
	dst := map[string]any{
		"a": "original",
		"b": map[string]any{"nested": "value"},
	}
	src := map[string]any{
		"a": "overridden",
		"c": "new",
	}

	mergeMaps(dst, src)

	if dst["a"] != "overridden" {
		t.Errorf("Expected 'a' to be 'overridden', got %v", dst["a"])
	}
	if dst["c"] != "new" {
		t.Errorf("Expected 'c' to be 'new', got %v", dst["c"])
	}

	// Test nested merge
	dst = map[string]any{
		"nested": map[string]any{
			"keep":     "original",
			"override": "old",
		},
	}
	src = map[string]any{
		"nested": map[string]any{
			"override": "new",
			"add":      "added",
		},
	}

	mergeMaps(dst, src)

	nested := dst["nested"].(map[string]any)
	if nested["keep"] != "original" {
		t.Errorf("Expected nested 'keep' to be 'original', got %v", nested["keep"])
	}
	if nested["override"] != "new" {
		t.Errorf("Expected nested 'override' to be 'new', got %v", nested["override"])
	}
	if nested["add"] != "added" {
		t.Errorf("Expected nested 'add' to be 'added', got %v", nested["add"])
	}

	// Test nil value handling
	dst = map[string]any{"key": "value"}
	src = map[string]any{"key": nil}

	mergeMaps(dst, src)

	if dst["key"] != "value" {
		t.Errorf("Expected 'key' to remain 'value' when src is nil, got %v", dst["key"])
	}
}
