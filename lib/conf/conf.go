// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"encoding/json"
	"io"
	"os"
)

// Configuration for single-sign-on (SSO).
type SSOConfig struct {
	Cert    string `json:"cert,omitempty"`
	CertKey string `json:"certKey,omitempty"`

	// If the certificate is self-signed, we need to skip verification.
	SelfSigned bool `json:"selfSigned,omitempty"`
}

type DBReconnectConfig struct {
	// The interval between liveness pings to the database.
	LivenessPingIntervalSeconds int `json:"livenessPingIntervalSeconds"`
	// The interval between reconnection attempts on connection loss.
	RetryIntervalSeconds int `json:"retryIntervalSeconds"`
	// The maximum number of reconnection attempts on connection loss before panic.
	MaxRetries int `json:"maxRetries"`
}

// Database configuration.
type DBConfig struct {
	Host      string            `json:"host"`
	Port      int               `json:"port"`
	Database  string            `json:"database"`
	User      string            `json:"user"`
	Password  string            `json:"password"`
	Reconnect DBReconnectConfig `json:"reconnect"`
}

// Configuration for the monitoring module.
type MonitoringConfig struct {
	// The labels to add to all metrics.
	Labels map[string]string `json:"labels"`

	// The port to expose the metrics on.
	Port int `json:"port"`
}

// Configuration for the keystone authentication.
type KeystoneConfig struct {
	// The URL of the keystone service.
	URL string `json:"url"`
	// Availability of the keystone service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
	// The OpenStack username (OS_USERNAME in openstack cli).
	OSUsername string `json:"username"`
	// The OpenStack password (OS_PASSWORD in openstack cli).
	OSPassword string `json:"password"`
	// The OpenStack project name (OS_PROJECT_NAME in openstack cli).
	OSProjectName string `json:"projectName"`
	// The OpenStack user domain name (OS_USER_DOMAIN_NAME in openstack cli).
	OSUserDomainName string `json:"userDomainName"`
	// The OpenStack project domain name (OS_PROJECT_DOMAIN_NAME in openstack cli).
	OSProjectDomainName string `json:"projectDomainName"`
}

// Configuration for the cortex service.
type Config interface {
	GetDBConfig() DBConfig
	GetMonitoringConfig() MonitoringConfig
	GetKeystoneConfig() KeystoneConfig
	// Check if the configuration is valid.
	Validate() error
}

// TODO: Strip this off everything we don't need anymore.
type SharedConfig struct {
	DBConfig         `json:"db"`
	MonitoringConfig `json:"monitoring"`
	KeystoneConfig   `json:"keystone"`
}

// Create a new configuration from the default config json file.
//
// This will read two files:
//   - /etc/config/conf.json
//   - /etc/secrets/secrets.json
//
// The values read from secrets.json will override the values in conf.json
func GetConfigOrDie[C any]() C {
	// Note: We need to read the config as a raw map first, to avoid golang
	// unmarshalling default values for the fields.

	// Read the base config from the configmap (not including secrets).
	cmConf, err := readRawConfig("/etc/config/conf.json")
	if err != nil {
		panic(err)
	}
	// Read the secrets config from the kubernetes secret.
	secretConf, err := readRawConfig("/etc/secrets/secrets.json")
	if err != nil {
		panic(err)
	}
	return newConfigFromMaps[C](cmConf, secretConf)
}

func newConfigFromMaps[C any](base, override map[string]any) C {
	// Merge the base config with the override config.
	mergedConf := mergeMaps(base, override)
	// Marshal again, and then unmarshal into the config struct.
	mergedBytes, err := json.Marshal(mergedConf)
	if err != nil {
		panic(err)
	}
	var c C
	if err := json.Unmarshal(mergedBytes, &c); err != nil {
		panic(err)
	}
	return c
}

// Read the json as a map from the given file path.
func readRawConfig(filepath string) (map[string]any, error) {
	file, err := os.Open(filepath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	bytes, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	return readRawConfigFromBytes(bytes)
}

func readRawConfigFromBytes(data []byte) (map[string]any, error) {
	var conf map[string]any
	if err := json.Unmarshal(data, &conf); err != nil {
		return nil, err
	}
	return conf, nil
}

// mergeMaps recursively overrides dst with src (in-place)
func mergeMaps(dst, src map[string]any) map[string]any {
	result := dst
	for k, v := range src {
		if v == nil {
			// If src value is nil, skip override
			continue
		}
		if dstVal, ok := dst[k]; ok {
			// If both are maps, merge recursively
			dstMap, dstIsMap := dstVal.(map[string]any)
			srcMap, srcIsMap := v.(map[string]any)
			if dstIsMap && srcIsMap {
				result[k] = mergeMaps(dstMap, srcMap)
				continue
			}
		}
		// Otherwise, override
		result[k] = v
	}
	return result
}

func (c *SharedConfig) GetDBConfig() DBConfig                 { return c.DBConfig }
func (c *SharedConfig) GetMonitoringConfig() MonitoringConfig { return c.MonitoringConfig }
func (c *SharedConfig) GetKeystoneConfig() KeystoneConfig     { return c.KeystoneConfig }
