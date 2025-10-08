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

// Metric configuration for the sync/prometheus module.
type SyncPrometheusMetricConfig struct {
	// The query to use to fetch the metric.
	Query string `json:"query"`
	// Especially when a more complex query is used, we need an alias
	// under which the table will be stored in the database.
	// Additionally, this alias is used to reference the metric in the
	// feature extractors as dependency.
	Alias string `json:"alias"`
	// The type of the metric, mapping directly to a metric model.
	Type string `json:"type"`

	TimeRangeSeconds  *int `json:"timeRangeSeconds,omitempty"`
	IntervalSeconds   *int `json:"intervalSeconds,omitempty"`
	ResolutionSeconds *int `json:"resolutionSeconds,omitempty"`
}

// Configuration for a single prometheus host.
type SyncPrometheusHostConfig struct {
	// The name of the prometheus host.
	Name string `json:"name"`
	// The URL of the prometheus host.
	URL string `json:"url"`
	// The SSO configuration for this host.
	SSO SSOConfig `json:"sso,omitempty"`
	// The types of metrics this host provides.
	ProvidedMetricTypes []string `json:"provides"`
}

// Configuration for the sync/prometheus module containing a list of metrics.
type SyncPrometheusConfig struct {
	Hosts   []SyncPrometheusHostConfig   `json:"hosts,omitempty"`
	Metrics []SyncPrometheusMetricConfig `json:"metrics,omitempty"`
}

// Configuration for the sync/openstack module.
type SyncOpenStackConfig struct {
	// Configuration for the nova service.
	Nova SyncOpenStackNovaConfig `json:"nova"`
	// Configuration for the placement service.
	Placement SyncOpenStackPlacementConfig `json:"placement"`
	// Configuration for the manila service.
	Manila SyncOpenStackManilaConfig `json:"manila"`
	// Configuration for the identity service.
	Identity SyncOpenStackIdentityConfig `json:"identity"`
	// Configuration for the limes service.
	Limes SyncOpenStackLimesConfig `json:"limes"`
	// Configuration for the cinder service.
	Cinder SyncOpenStackCinderConfig `json:"cinder"`
}

// Configuration for the nova service.
type SyncOpenStackNovaConfig struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
	// The types of resources to sync.
	Types []string `json:"types"`
	// Time frame in minutes for the changes-since parameter when fetching deleted servers.
	DeletedServersChangesSinceMinutes *int `json:"deletedServersChangesSinceMinutes,omitempty"`
}

// Configuration for the placement service.
type SyncOpenStackPlacementConfig struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
	// The types of resources to sync.
	Types []string `json:"types"`
}

// Configuration for the manila service.
type SyncOpenStackManilaConfig struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
	// The types of resources to sync.
	Types []string `json:"types"`
}

// Configuration for the cinder service
type SyncOpenStackCinderConfig struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
	// The types of resources to sync.
	Types []string `json:"types"`
}

// Configuration for the identity service.
type SyncOpenStackIdentityConfig struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
	// The types of resources to sync.
	Types []string `json:"types"`
}

// Configuration for the limes service.
type SyncOpenStackLimesConfig struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
	// The types of resources to sync.
	Types []string `json:"types"`
}

// Configuration for the sync module.
type SyncConfig struct {
	Prometheus SyncPrometheusConfig `json:"prometheus"`
	OpenStack  SyncOpenStackConfig  `json:"openstack"`
}

type FeatureExtractorConfig struct {
	// The name of the extractor.
	Name string `json:"name"`
	// Custom options for the extractor, as a raw yaml map.
	Options RawOpts `json:"options,omitempty"`
	// The dependencies this extractor needs.
	DependencyConfig `json:"dependencies,omitempty"`
	// Recency that tells how old a feature needs to be to be recalculated
	RecencySeconds *int `json:"recencySeconds,omitempty"`
	// MQTT topic to publish the features to.
	// If not set, the extractor will not publish features to MQTT.
	MQTTTopic string `json:"mqttTopic,omitempty"`
}

// Configuration for the features module.
type ExtractorConfig struct {
	Plugins []FeatureExtractorConfig `json:"plugins"`
}

type NovaHypervisorType = string

const (
	NovaHypervisorTypeQEMU   NovaHypervisorType = "QEMU"
	NovaHypervisorTypeCH     NovaHypervisorType = "CH" // Cloud hypervisor
	NovaHypervisorTypeVMware NovaHypervisorType = "VMware vCenter Server"
	NovaHypervisorTypeIronic NovaHypervisorType = "ironic"
)

// Configuration for the descheduler module.
type DeschedulerConfig struct {
	Nova NovaDeschedulerConfig `json:"nova"`
}

// Configuration for the nova descheduler.
type NovaDeschedulerConfig struct {
	// The availability of the nova service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
	// The steps to execute in the descheduler.
	Plugins []DeschedulerStepConfig `json:"plugins"`
	// If dry-run is disabled (by default its enabled).
	DisableDryRun bool `json:"disableDryRun,omitempty"`
}

type DeschedulerStepConfig struct {
	// The name of the step.
	Name string `json:"name"`
	// Custom options for the step, as a raw yaml map.
	Options RawOpts `json:"options,omitempty"`
	// The dependencies this step needs.
	DependencyConfig `json:"dependencies,omitempty"`
}

// Configuration for the kpis module.
type KPIsConfig struct {
	// KPI plugins to use.
	Plugins []KPIPluginConfig `json:"plugins"`
}

// Configuration for a single KPI plugin.
type KPIPluginConfig struct {
	// The name of the KPI plugin.
	Name string `json:"name"`
	// Custom options for the KPI plugin, as a raw json map.
	Options RawOpts `json:"options,omitempty"`
	// The dependencies this KPI plugin needs.
	DependencyConfig `json:"dependencies,omitempty"`
}

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

// Configuration for the keystone authentication.
type KeystoneConfig struct {
	// The URL of the keystone service.
	URL string `json:"url"`
	// The SSO certificate to use. If none is given, we won't
	// use SSO to connect to the openstack services.
	SSO SSOConfig `json:"sso,omitempty"`
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
	GetSyncConfig() SyncConfig
	GetExtractorConfig() ExtractorConfig
	GetDeschedulerConfig() DeschedulerConfig
	GetKPIsConfig() KPIsConfig
	GetMonitoringConfig() MonitoringConfig
	GetMQTTConfig() MQTTConfig
	GetAPIConfig() APIConfig
	GetKeystoneConfig() KeystoneConfig
	// Check if the configuration is valid.
	Validate() error
}

type SharedConfig struct {
	LoggingConfig     `json:"logging"`
	DBConfig          `json:"db"`
	SyncConfig        `json:"sync"`
	ExtractorConfig   `json:"extractor"`
	DeschedulerConfig `json:"descheduler"`
	MonitoringConfig  `json:"monitoring"`
	KPIsConfig        `json:"kpis"`
	MQTTConfig        `json:"mqtt"`
	APIConfig         `json:"api"`
	KeystoneConfig    `json:"keystone"`
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

func (c *SharedConfig) GetLoggingConfig() LoggingConfig         { return c.LoggingConfig }
func (c *SharedConfig) GetDBConfig() DBConfig                   { return c.DBConfig }
func (c *SharedConfig) GetSyncConfig() SyncConfig               { return c.SyncConfig }
func (c *SharedConfig) GetExtractorConfig() ExtractorConfig     { return c.ExtractorConfig }
func (c *SharedConfig) GetDeschedulerConfig() DeschedulerConfig { return c.DeschedulerConfig }
func (c *SharedConfig) GetKPIsConfig() KPIsConfig               { return c.KPIsConfig }
func (c *SharedConfig) GetMonitoringConfig() MonitoringConfig   { return c.MonitoringConfig }
func (c *SharedConfig) GetMQTTConfig() MQTTConfig               { return c.MQTTConfig }
func (c *SharedConfig) GetAPIConfig() APIConfig                 { return c.APIConfig }
func (c *SharedConfig) GetKeystoneConfig() KeystoneConfig       { return c.KeystoneConfig }
