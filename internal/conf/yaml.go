// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// Configuration for single-sign-on (SSO).
type SSOConfig struct {
	Cert    string `yaml:"cert,omitempty"`
	CertKey string `yaml:"certKey,omitempty"`

	// If the certificate is self-signed, we need to skip verification.
	SelfSigned bool `yaml:"selfSigned,omitempty"`
}

// Configuration for structured logging.
type LoggingConfig struct {
	// The log level to use (debug, info, warn, error).
	LevelStr string `yaml:"level"`
	// The log format to use (json, text).
	Format string `yaml:"format"`
}

// Database configuration.
type DBConfig struct {
	Host     string `yaml:"host"`
	Port     string `yaml:"port"`
	Database string `yaml:"database"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
}

// Metric configuration for the sync/prometheus module.
type SyncPrometheusMetricConfig struct {
	// The query to use to fetch the metric.
	Query string `yaml:"query"`
	// Especially when a more complex query is used, we need an alias
	// under which the table will be stored in the database.
	// Additionally, this alias is used to reference the metric in the
	// feature extractors as dependency.
	Alias string `yaml:"alias"`
	// The type of the metric, mapping directly to a metric model.
	Type string `yaml:"type"`

	TimeRangeSeconds  *int `yaml:"timeRangeSeconds,omitempty"`
	IntervalSeconds   *int `yaml:"intervalSeconds,omitempty"`
	ResolutionSeconds *int `yaml:"resolutionSeconds,omitempty"`
}

// Configuration for a single prometheus host.
type SyncPrometheusHostConfig struct {
	// The name of the prometheus host.
	Name string `yaml:"name"`
	// The URL of the prometheus host.
	URL string `yaml:"url"`
	// The SSO configuration for this host.
	SSO SSOConfig `yaml:"sso,omitempty"`
	// The types of metrics this host provides.
	ProvidedMetricTypes []string `yaml:"provides"`
}

// Configuration for the sync/prometheus module containing a list of metrics.
type SyncPrometheusConfig struct {
	Hosts   []SyncPrometheusHostConfig   `yaml:"hosts,omitempty"`
	Metrics []SyncPrometheusMetricConfig `yaml:"metrics,omitempty"`
}

// Configuration for the sync/openstack module.
type SyncOpenStackConfig struct {
	// Configuration for the keystone service.
	Keystone SyncOpenStackKeystoneConfig `yaml:"keystone"`
	// Configuration for the nova service.
	Nova SyncOpenStackNovaConfig `yaml:"nova"`
	// Configuration for the placement service.
	Placement SyncOpenStackPlacementConfig `yaml:"placement"`
}

// Configuration for the keystone authentication.
type SyncOpenStackKeystoneConfig struct {
	// The URL of the keystone service.
	URL string `yaml:"url"`
	// The SSO certificate to use. If none is given, we won't
	// use SSO to connect to the openstack services.
	SSO SSOConfig `yaml:"sso,omitempty"`
	// The OpenStack username (OS_USERNAME in openstack cli).
	OSUsername string `yaml:"username"`
	// The OpenStack password (OS_PASSWORD in openstack cli).
	OSPassword string `yaml:"password"`
	// The OpenStack project name (OS_PROJECT_NAME in openstack cli).
	OSProjectName string `yaml:"projectName"`
	// The OpenStack user domain name (OS_USER_DOMAIN_NAME in openstack cli).
	OSUserDomainName string `yaml:"userDomainName"`
	// The OpenStack project domain name (OS_PROJECT_DOMAIN_NAME in openstack cli).
	OSProjectDomainName string `yaml:"projectDomainName"`
}

// Configuration for the nova service.
type SyncOpenStackNovaConfig struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `yaml:"availability"`
	// The types of resources to sync.
	Types []string `yaml:"types"`
}

// Configuration for the placement service.
type SyncOpenStackPlacementConfig struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `yaml:"availability"`
	// The types of resources to sync.
	Types []string `yaml:"types"`
}

// Configuration for the sync module.
type SyncConfig struct {
	Prometheus SyncPrometheusConfig `yaml:"prometheus"`
	OpenStack  SyncOpenStackConfig  `yaml:"openstack"`
}

type FeatureExtractorConfig struct {
	// The name of the extractor.
	Name string `yaml:"name"`
	// Custom options for the extractor, as a raw yaml map.
	Options RawOpts `yaml:"options,omitempty"`
	// The dependencies this extractor needs.
	DependencyConfig `yaml:"dependencies,omitempty"`
}

// Configuration for the features module.
type FeaturesConfig struct {
	Extractors []FeatureExtractorConfig `yaml:"extractors"`
}

type SchedulerStepConfig struct {
	// The name of the step.
	Name string `yaml:"name"`
	// Custom options for the step, as a raw yaml map.
	Options RawOpts `yaml:"options,omitempty"`
	// The dependencies this step needs.
	DependencyConfig `yaml:"dependencies,omitempty"`
	// The validations to use for this step.
	DisabledValidations SchedulerStepDisabledValidationsConfig `yaml:"disabledValidations,omitempty"`
}

// Config for which validations to disable for a scheduler step.
type SchedulerStepDisabledValidationsConfig struct {
	// Whether to validate that no hosts are removed or added from the scheduler
	// step. This should only be disabled for scheduler steps that remove hosts.
	// Thus, if no value is provided, the default is false.
	SameHostNumberInOut bool `yaml:"sameHostNumberInOut,omitempty"`
}

// Configuration for the scheduler module.
type SchedulerConfig struct {
	// Scheduler steps by their name.
	Steps []SchedulerStepConfig `yaml:"steps"`

	API SchedulerAPIConfig `yaml:"api"`
}

// Configuration for the scheduler API.
type SchedulerAPIConfig struct {
	// If request bodies should be logged out.
	// This feature is intended for debugging purposes only.
	LogRequestBodies bool `yaml:"logRequestBodies"`

	// The port to use for the scheduler API.
	Port int `yaml:"port"`
}

// Configuration for the monitoring module.
type MonitoringConfig struct {
	// The labels to add to all metrics.
	Labels map[string]string `yaml:"labels"`

	// The port to expose the metrics on.
	Port int `yaml:"port"`
}

// Configuration for the mqtt client.
type MQTTConfig struct {
	// The URL of the MQTT broker to use for mqtt.
	URL string `yaml:"url"`
	// Credentials for the MQTT broker.
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// Configuration for the cortex service.
type Config interface {
	GetLoggingConfig() LoggingConfig
	GetDBConfig() DBConfig
	GetSyncConfig() SyncConfig
	GetFeaturesConfig() FeaturesConfig
	GetSchedulerConfig() SchedulerConfig
	GetMonitoringConfig() MonitoringConfig
	GetMQTTConfig() MQTTConfig
	// Check if the configuration is valid.
	Validate() error
}

type config struct {
	LoggingConfig    `yaml:"logging"`
	DBConfig         `yaml:"db"`
	SyncConfig       `yaml:"sync"`
	FeaturesConfig   `yaml:"features"`
	SchedulerConfig  `yaml:"scheduler"`
	MonitoringConfig `yaml:"monitoring"`
	MQTTConfig       `yaml:"mqtt"`
}

// Create a new configuration from the default config yaml file.
func NewConfig() Config {
	return newConfigFromFile("/etc/config/conf.yaml")
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
	if err := yaml.Unmarshal(bytes, &c); err != nil {
		panic(err)
	}
	return &c
}

func (c *config) GetLoggingConfig() LoggingConfig       { return c.LoggingConfig }
func (c *config) GetDBConfig() DBConfig                 { return c.DBConfig }
func (c *config) GetSyncConfig() SyncConfig             { return c.SyncConfig }
func (c *config) GetFeaturesConfig() FeaturesConfig     { return c.FeaturesConfig }
func (c *config) GetSchedulerConfig() SchedulerConfig   { return c.SchedulerConfig }
func (c *config) GetMonitoringConfig() MonitoringConfig { return c.MonitoringConfig }
func (c *config) GetMQTTConfig() MQTTConfig             { return c.MQTTConfig }
