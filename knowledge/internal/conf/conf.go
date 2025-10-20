// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	libconf "github.com/cobaltcore-dev/cortex/lib/conf"
)

type FeatureExtractorConfig struct {
	// The name of the extractor.
	Name string `json:"name"`
	// Custom options for the extractor, as a raw yaml map.
	Options libconf.RawOpts `json:"options,omitempty"`
	// The dependencies this extractor needs.
	libconf.DependencyConfig `json:"dependencies,omitempty"`
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

// Metric configuration for the datasource/prometheus module.
type DatasourcePrometheusMetricConfig struct {
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
type DatasourcePrometheusHostConfig struct {
	// The name of the prometheus host.
	Name string `json:"name"`
	// The URL of the prometheus host.
	URL string `json:"url"`
	// The SSO configuration for this host.
	SSO libconf.SSOConfig `json:"sso,omitempty"`
	// The types of metrics this host provides.
	ProvidedMetricTypes []string `json:"provides"`
}

// Configuration for the datasource/prometheus module containing a list of metrics.
type DatasourcePrometheusConfig struct {
	Hosts   []DatasourcePrometheusHostConfig   `json:"hosts,omitempty"`
	Metrics []DatasourcePrometheusMetricConfig `json:"metrics,omitempty"`
}

// Configuration for the datasource/openstack module.
type DatasourceOpenStackConfig struct {
	// Configuration for the nova service.
	Nova DatasourceOpenStackNovaConfig `json:"nova"`
	// Configuration for the placement service.
	Placement DatasourceOpenStackPlacementConfig `json:"placement"`
	// Configuration for the manila service.
	Manila DatasourceOpenStackManilaConfig `json:"manila"`
	// Configuration for the identity service.
	Identity DatasourceOpenStackIdentityConfig `json:"identity"`
	// Configuration for the limes service.
	Limes DatasourceOpenStackLimesConfig `json:"limes"`
	// Configuration for the cinder service.
	Cinder DatasourceOpenStackCinderConfig `json:"cinder"`
}

// Configuration for the nova service.
type DatasourceOpenStackNovaConfig struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
	// The types of resources to sync.
	Types []string `json:"types"`
	// Time frame in minutes for the changes-since parameter when fetching deleted servers.
	DeletedServersChangesSinceMinutes *int `json:"deletedServersChangesSinceMinutes,omitempty"`
}

// Configuration for the placement service.
type DatasourceOpenStackPlacementConfig struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
	// The types of resources to sync.
	Types []string `json:"types"`
}

// Configuration for the manila service.
type DatasourceOpenStackManilaConfig struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
	// The types of resources to sync.
	Types []string `json:"types"`
}

// Configuration for the cinder service
type DatasourceOpenStackCinderConfig struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
	// The types of resources to sync.
	Types []string `json:"types"`
}

// Configuration for the identity service.
type DatasourceOpenStackIdentityConfig struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
	// The types of resources to sync.
	Types []string `json:"types"`
}

// Configuration for the limes service.
type DatasourceOpenStackLimesConfig struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
	// The types of resources to sync.
	Types []string `json:"types"`
}

// Configuration for the datasource module.
type DatasourceConfig struct {
	Prometheus DatasourcePrometheusConfig `json:"prometheus"`
	OpenStack  DatasourceOpenStackConfig  `json:"openstack"`
}

type Config struct {
	ExtractorConfig  `json:"extractor"`
	DatasourceConfig `json:"datasource"`

	// Lib modules configs.
	libconf.MonitoringConfig `json:"monitoring"`
	libconf.LoggingConfig    `json:"logging"`
	libconf.DBConfig         `json:"db"`

	// Generally needed to expose an /up endpoint.
	libconf.APIConfig `json:"api"`
	// Needed to connect to OpenStack.
	libconf.KeystoneConfig `json:"keystone"`
}
