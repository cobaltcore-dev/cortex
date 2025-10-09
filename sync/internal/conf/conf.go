// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	libconf "github.com/cobaltcore-dev/cortex/lib/conf"
)

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
	SSO libconf.SSOConfig `json:"sso,omitempty"`
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

type Config struct {
	SyncConfig `json:"sync"`

	// Lib modules configs.
	libconf.MonitoringConfig `json:"monitoring"`
	libconf.LoggingConfig    `json:"logging"`
	libconf.DBConfig         `json:"db"`
	libconf.MQTTConfig       `json:"mqtt"`

	libconf.KeystoneConfig `json:"keystone"`
	libconf.APIConfig      `json:"api"`
}
