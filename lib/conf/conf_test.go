// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
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

func TestGetConfigOrDie(t *testing.T) {
	content := `
{
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
