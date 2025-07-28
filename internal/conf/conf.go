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

// Configuration for the manila service.
type SyncOpenStackManilaConfig struct {
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

type ManilaSchedulerConfig struct {
	// Scheduler step plugins by their name.
	Plugins []SchedulerStepConfig `json:"plugins"`
}

type NovaSchedulerConfig struct {
	// Scheduler step plugins by their name.
	Plugins []SchedulerStepConfig `json:"plugins"`

	// Dependencies needed by all the Nova scheduler steps.
	DependencyConfig `json:"dependencies,omitempty"`
}

type SchedulerStepConfig struct {
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

	// Additional nova configuration for the step.

	// The scope of the step, i.e. which hosts it should be applied to.
	Scope *NovaSchedulerStepScope `json:"scope,omitempty"`
}

// Scope that defines which hosts a scheduler step should be applied to.
// In addition, it also defines the traits for which the step should be applied.
type NovaSchedulerStepScope struct {
	// Selectors applied to the compute hosts.
	HostSelectors []NovaSchedulerStepHostSelector `json:"hostSelectors,omitempty"`
	// Selectors applied to the given nova spec.
	SpecSelectors []NovaSchedulerStepSpecSelector `json:"specSelectors,omitempty"`
}

type NovaSchedulerStepHostSelector struct {
	// One of: "trait", "hypervisorType"
	Subject string `json:"subject"`
	// Selector type, currently only "infix" is supported.
	Type string `json:"type,omitempty"`
	// Value of the selector (typed to the given type).
	Value any `json:"value,omitempty"`
	// How the selector should be applied:
	// Let A be the previous set of hosts, and B the scoped hosts.
	// - "union" means that the scoped hosts are added to the previous set of hosts.
	// - "difference" means that the scoped hosts are removed from the previous set of hosts.
	// - "intersection" means that the scoped hosts are the only ones that remain in the previous set of hosts.
	Operation string `json:"operation,omitempty"`
}

type NovaSchedulerStepSpecSelector struct {
	// One of: "flavor", "vmware"
	Subject string `json:"subject"`
	// Selector type: bool, infix.
	Type string `json:"type,omitempty"`
	// Value of the selector (typed to the given type).
	Value any `json:"value,omitempty"`
	// What to do if the selector is matched:
	// - "skip" means that the step is skipped.
	// - "continue" means that the step is applied.
	Action string `json:"action,omitempty"`
}

type NovaSchedulerStepHostCapabilities struct {
	// If given, the scheduler step will only be applied to hosts
	// that have ONE of the given traits.
	AnyOfTraitInfixes []string `json:"anyOfTraitInfixes,omitempty"`
	// If given, the scheduler step will only be applied to hosts
	// that have ONE of the given hypervisor types.
	AnyOfHypervisorTypeInfixes []string `json:"anyOfHypervisorTypeInfixes,omitempty"`
	// If given, the scheduler step will only be applied to hosts
	// that have ALL of the given traits.
	AllOfTraitInfixes []string `json:"allOfTraitInfixes,omitempty"`

	// If the selection should be inverted, i.e. the step should be applied to hosts
	// that do NOT match the aforementioned criteria.
	InvertSelection bool `json:"invertSelection,omitempty"`
}

func (s NovaSchedulerStepHostCapabilities) IsUndefined() bool {
	return len(s.AnyOfTraitInfixes) == 0 && len(s.AnyOfHypervisorTypeInfixes) == 0 && len(s.AllOfTraitInfixes) == 0
}

type NovaSchedulerStepSpecScope struct {
	// If given, the scheduler step will only be applied to specs
	// that contain ALL of the following infixes.
	AllOfFlavorNameInfixes []string `json:"allOfFlavorNameInfixes,omitempty"`
}

func (s NovaSchedulerStepSpecScope) IsUndefined() bool {
	return len(s.AllOfFlavorNameInfixes) == 0
}

// Config for which validations to disable for a scheduler step.
type SchedulerStepDisabledValidationsConfig struct {
	// Whether to validate that no subjects are removed or added from the scheduler
	// step. This should only be disabled for scheduler steps that remove subjects.
	// Thus, if no value is provided, the default is false.
	SameSubjectNumberInOut bool `json:"sameSubjectNumberInOut,omitempty"`
}

// Configuration for the scheduler module.
type SchedulerConfig struct {
	Nova   NovaSchedulerConfig   `json:"nova"`
	Manila ManilaSchedulerConfig `json:"manila"`

	API SchedulerAPIConfig `json:"api"`
}

// Configuration for the scheduler API.
type SchedulerAPIConfig struct {
	// If request bodies should be logged out.
	// This feature is intended for debugging purposes only.
	LogRequestBodies bool `json:"logRequestBodies"`
}

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
	GetChecks() []string
	GetLoggingConfig() LoggingConfig
	GetDBConfig() DBConfig
	GetSyncConfig() SyncConfig
	GetExtractorConfig() ExtractorConfig
	GetSchedulerConfig() SchedulerConfig
	GetDeschedulerConfig() DeschedulerConfig
	GetKPIsConfig() KPIsConfig
	GetMonitoringConfig() MonitoringConfig
	GetMQTTConfig() MQTTConfig
	GetAPIConfig() APIConfig
	GetKeystoneConfig() KeystoneConfig
	// Check if the configuration is valid.
	Validate() error
}

type config struct {
	// The checks to run, in this particular order.
	Checks []string `json:"checks"`

	LoggingConfig     `json:"logging"`
	DBConfig          `json:"db"`
	SyncConfig        `json:"sync"`
	ExtractorConfig   `json:"extractor"`
	SchedulerConfig   `json:"scheduler"`
	DeschedulerConfig `json:"descheduler"`
	MonitoringConfig  `json:"monitoring"`
	KPIsConfig        `json:"kpis"`
	MQTTConfig        `json:"mqtt"`
	APIConfig         `json:"api"`
	KeystoneConfig    `json:"keystone"`
}

// Create a new configuration from the default config json file.
// Also inject environment variables into the configuration.
func NewConfig() Config {
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
	return newConfigFromMaps(cmConf, secretConf)
}

func newConfigFromMaps(base, override map[string]any) Config {
	// Merge the base config with the override config.
	mergedConf := mergeMaps(base, override)
	// Marshal again, and then unmarshal into the config struct.
	mergedBytes, err := json.Marshal(mergedConf)
	if err != nil {
		panic(err)
	}
	var c config
	if err := json.Unmarshal(mergedBytes, &c); err != nil {
		panic(err)
	}
	return &c
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

func (c *config) GetChecks() []string                     { return c.Checks }
func (c *config) GetLoggingConfig() LoggingConfig         { return c.LoggingConfig }
func (c *config) GetDBConfig() DBConfig                   { return c.DBConfig }
func (c *config) GetSyncConfig() SyncConfig               { return c.SyncConfig }
func (c *config) GetExtractorConfig() ExtractorConfig     { return c.ExtractorConfig }
func (c *config) GetSchedulerConfig() SchedulerConfig     { return c.SchedulerConfig }
func (c *config) GetDeschedulerConfig() DeschedulerConfig { return c.DeschedulerConfig }
func (c *config) GetKPIsConfig() KPIsConfig               { return c.KPIsConfig }
func (c *config) GetMonitoringConfig() MonitoringConfig   { return c.MonitoringConfig }
func (c *config) GetMQTTConfig() MQTTConfig               { return c.MQTTConfig }
func (c *config) GetAPIConfig() APIConfig                 { return c.APIConfig }
func (c *config) GetKeystoneConfig() KeystoneConfig       { return c.KeystoneConfig }
