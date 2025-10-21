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

// Configuration for structured logging.
type LoggingConfig struct {
	// The log level to use (debug, info, warn, error).
	LevelStr string `json:"level"`
	// The log format to use (json, text).
	Format string `json:"format"`
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

type NovaHypervisorType = string

const (
	NovaHypervisorTypeQEMU   NovaHypervisorType = "QEMU"
	NovaHypervisorTypeCH     NovaHypervisorType = "CH" // Cloud hypervisor
	NovaHypervisorTypeVMware NovaHypervisorType = "VMware vCenter Server"
	NovaHypervisorTypeIronic NovaHypervisorType = "ironic"
)

// Configuration for the monitoring module.
type MonitoringConfig struct {
	// The labels to add to all metrics.
	Labels map[string]string `json:"labels"`

	// The port to expose the metrics on.
	Port int `json:"port"`
}

type MQTTReconnectConfig struct {
	// The interval between reconnection attempts on connection loss.
	RetryIntervalSeconds int `json:"retryIntervalSeconds"`

	// The maximum number of reconnection attempts on connection loss before panic.
	MaxRetries int `json:"maxRetries"`
}

// Configuration for the mqtt client.
type MQTTConfig struct {
	// The URL of the MQTT broker to use for mqtt.
	URL string `json:"url"`
	// Credentials for the MQTT broker.
	Username  string              `json:"username"`
	Password  string              `json:"password"`
	Reconnect MQTTReconnectConfig `json:"reconnect"`
}

// Configuration for the api port.
type APIConfig struct {
	// The port to expose the API on.
	Port int `json:"port"`
}

type SchedulerStepConfig[Extra any] struct {
	// The name of the step implementation.
	Name string `json:"name"`
	// The alias of this step, if any.
	//
	// The alias can be used to distinguish between different configurations
	// of the same step, or use a more specific name.
	Alias string `json:"alias,omitempty"`
	// Custom options for the step, as a raw yaml map.
	Options RawOpts `json:"options,omitempty"`
	// The dependencies this step needs.
	DependencyConfig `json:"dependencies,omitempty"`
	// The validations to use for this step.
	DisabledValidations SchedulerStepDisabledValidationsConfig `json:"disabledValidations,omitempty"`

	// Additional configuration for the step, if needed.
	Extra *Extra `json:"extra,omitempty"`
}

// Config for which validations to disable for a scheduler step.
type SchedulerStepDisabledValidationsConfig struct {
	// Whether to validate that no subjects are removed or added from the scheduler
	// step. This should only be disabled for scheduler steps that remove subjects.
	// Thus, if no value is provided, the default is false.
	SameSubjectNumberInOut bool `json:"sameSubjectNumberInOut,omitempty"`
	// Whether to validate that, after running the step, there are remaining subjects.
	// This should only be disabled for scheduler steps that are expected to
	// remove all subjects.
	SomeSubjectsRemain bool `json:"someSubjectsRemain,omitempty"`
}

// Configuration for the scheduler API.
type SchedulerAPIConfig struct {
	// If request bodies should be logged out.
	// This feature is intended for debugging purposes only.
	LogRequestBodies bool `json:"logRequestBodies"`
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
	GetLoggingConfig() LoggingConfig
	GetDBConfig() DBConfig
	GetMonitoringConfig() MonitoringConfig
	GetMQTTConfig() MQTTConfig
	GetAPIConfig() APIConfig
	GetKeystoneConfig() KeystoneConfig
	// Check if the configuration is valid.
	Validate() error
}

type SharedConfig struct {
	LoggingConfig    `json:"logging"`
	DBConfig         `json:"db"`
	MonitoringConfig `json:"monitoring"`
	MQTTConfig       `json:"mqtt"`
	APIConfig        `json:"api"`
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

func (c *SharedConfig) GetLoggingConfig() LoggingConfig       { return c.LoggingConfig }
func (c *SharedConfig) GetDBConfig() DBConfig                 { return c.DBConfig }
func (c *SharedConfig) GetMonitoringConfig() MonitoringConfig { return c.MonitoringConfig }
func (c *SharedConfig) GetMQTTConfig() MQTTConfig             { return c.MQTTConfig }
func (c *SharedConfig) GetAPIConfig() APIConfig               { return c.APIConfig }
func (c *SharedConfig) GetKeystoneConfig() KeystoneConfig     { return c.KeystoneConfig }
