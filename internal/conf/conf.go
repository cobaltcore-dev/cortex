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
	// Configuration for the keystone service.
	Keystone SyncOpenStackKeystoneConfig `json:"keystone"`
	// Configuration for the nova service.
	Nova SyncOpenStackNovaConfig `json:"nova"`
	// Configuration for the placement service.
	Placement SyncOpenStackPlacementConfig `json:"placement"`
}

// Configuration for the keystone authentication.
type SyncOpenStackKeystoneConfig struct {
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

// Configuration for the nova service.
type SyncOpenStackNovaConfig struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
	// The types of resources to sync.
	Types []string `json:"types"`
}

// Configuration for the placement service.
type SyncOpenStackPlacementConfig struct {
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
}

// Configuration for the features module.
type ExtractorConfig struct {
	Plugins []FeatureExtractorConfig `json:"plugins"`
}

type NovaSchedulerConfig struct {
	// Scheduler step plugins by their name.
	Plugins []NovaSchedulerStepConfig `json:"plugins"`
}

type NovaSchedulerStepConfig struct {
	// The name of the step.
	Name string `json:"name"`
	// Custom options for the step, as a raw yaml map.
	Options RawOpts `json:"options,omitempty"`
	// The dependencies this step needs.
	DependencyConfig `json:"dependencies,omitempty"`
	// The validations to use for this step.
	DisabledValidations NovaSchedulerStepDisabledValidationsConfig `json:"disabledValidations,omitempty"`
}

// Config for which validations to disable for a scheduler step.
type NovaSchedulerStepDisabledValidationsConfig struct {
	// Whether to validate that no hosts are removed or added from the scheduler
	// step. This should only be disabled for scheduler steps that remove hosts.
	// Thus, if no value is provided, the default is false.
	SameHostNumberInOut bool `json:"sameHostNumberInOut,omitempty"`
}

// Configuration for the scheduler module.
type SchedulerConfig struct {
	Nova NovaSchedulerConfig `json:"nova"`

	API SchedulerAPIConfig `json:"api"`
}

// Configuration for the scheduler API.
type SchedulerAPIConfig struct {
	// If request bodies should be logged out.
	// This feature is intended for debugging purposes only.
	LogRequestBodies bool `json:"logRequestBodies"`
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

// Configuration for the cortex service.
type Config interface {
	GetLoggingConfig() LoggingConfig
	GetDBConfig() DBConfig
	GetSyncConfig() SyncConfig
	GetExtractorConfig() ExtractorConfig
	GetSchedulerConfig() SchedulerConfig
	GetKPIsConfig() KPIsConfig
	GetMonitoringConfig() MonitoringConfig
	GetMQTTConfig() MQTTConfig
	GetAPIConfig() APIConfig
	// Check if the configuration is valid.
	Validate() error
}

type config struct {
	LoggingConfig    `json:"logging"`
	DBConfig         `json:"db"`
	SyncConfig       `json:"sync"`
	ExtractorConfig  `json:"extractor"`
	SchedulerConfig  `json:"scheduler"`
	MonitoringConfig `json:"monitoring"`
	KPIsConfig       `json:"kpis"`
	MQTTConfig       `json:"mqtt"`
	APIConfig        `json:"api"`
}

// Create a new configuration from the default config json file.
func NewConfig() Config {
	return newConfigFromFile("/etc/config/conf.json")
}

// Create a new configuration from the given file.
func newConfigFromFile(filepath string) Config {
	file, err := os.Open(filepath)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	bytes, err := io.ReadAll(file)
	if err != nil {
		panic(err)
	}
	return newConfigFromBytes(bytes)
}

// Create a new configuration from the given bytes.
func newConfigFromBytes(bytes []byte) Config {
	var c config
	if err := json.Unmarshal(bytes, &c); err != nil {
		panic(err)
	}
	return &c
}

func (c *config) GetLoggingConfig() LoggingConfig       { return c.LoggingConfig }
func (c *config) GetDBConfig() DBConfig                 { return c.DBConfig }
func (c *config) GetSyncConfig() SyncConfig             { return c.SyncConfig }
func (c *config) GetExtractorConfig() ExtractorConfig   { return c.ExtractorConfig }
func (c *config) GetSchedulerConfig() SchedulerConfig   { return c.SchedulerConfig }
func (c *config) GetKPIsConfig() KPIsConfig             { return c.KPIsConfig }
func (c *config) GetMonitoringConfig() MonitoringConfig { return c.MonitoringConfig }
func (c *config) GetMQTTConfig() MQTTConfig             { return c.MQTTConfig }
func (c *config) GetAPIConfig() APIConfig               { return c.APIConfig }
