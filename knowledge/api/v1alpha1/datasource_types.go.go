// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type PrometheusDatasource struct {
	// The query to use to fetch the metric.
	Query string `json:"query"`
	// Especially when a more complex query is used, we need an alias
	// under which the table will be stored in the database.
	// Additionally, this alias is used to reference the metric in the
	// feature extractors as dependency.
	Alias string `json:"alias"`
	// The type of the metric, mapping directly to a metric model supported
	// by cortex. Note that the metrics are fetched as time series, not instant.
	Type string `json:"type"`

	// The name of the prometheus host.
	HostName string `json:"hostName"`
	// The URL of the prometheus host.
	HostURL string `json:"hostURL"`

	// Time range in seconds to query the data for.
	TimeRangeSeconds *int `json:"timeRangeSeconds,omitempty"`
	// The interval at which to query the data.
	IntervalSeconds *int `json:"intervalSeconds,omitempty"`
	// The resolution of the data in seconds.
	ResolutionSeconds *int `json:"resolutionSeconds,omitempty"`
}

type NovaDatasource struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
	// The types of resources to sync.
	Types []string `json:"types"`
	// Time frame in minutes for the changes-since parameter when fetching deleted servers.
	DeletedServersChangesSinceMinutes *int `json:"deletedServersChangesSinceMinutes,omitempty"`
}

type PlacementDatasource struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
	// The types of resources to sync.
	Types []string `json:"types"`
}

type ManilaDatasource struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
	// The types of resources to sync.
	Types []string `json:"types"`
}

type IdentityDatasource struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
	// The types of resources to sync.
	Types []string `json:"types"`
}

type LimesDatasource struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
	// The types of resources to sync.
	Types []string `json:"types"`
}

type CinderDatasource struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
	// The types of resources to sync.
	Types []string `json:"types"`
}

type OpenStackDatasourceType string

const (
	// OpenStackDatasourceTypeNova indicates a Nova datasource.
	OpenStackDatasourceTypeNova OpenStackDatasourceType = "nova"
	// OpenStackDatasourceTypePlacement indicates a Placement datasource.
	OpenStackDatasourceTypePlacement OpenStackDatasourceType = "placement"
	// OpenStackDatasourceTypeManila indicates a Manila datasource.
	OpenStackDatasourceTypeManila OpenStackDatasourceType = "manila"
	// OpenStackDatasourceTypeIdentity indicates an Identity datasource.
	OpenStackDatasourceTypeIdentity OpenStackDatasourceType = "identity"
	// OpenStackDatasourceTypeLimes indicates a Limes datasource.
	OpenStackDatasourceTypeLimes OpenStackDatasourceType = "limes"
	// OpenStackDatasourceTypeCinder indicates a Cinder datasource.
	OpenStackDatasourceTypeCinder OpenStackDatasourceType = "cinder"
)

type OpenStackDatasource struct {
	// The type of the OpenStack datasource.
	Type OpenStackDatasourceType `json:"type"`

	// Datasource for openstack nova.
	// Only required if Type is "nova".
	Nova *NovaDatasource `json:"nova"`
	// Datasource for openstack placement.
	// Only required if Type is "placement".
	Placement *PlacementDatasource `json:"placement"`
	// Datasource for openstack manila.
	// Only required if Type is "manila".
	Manila *ManilaDatasource `json:"manila"`
	// Datasource for openstack identity.
	// Only required if Type is "identity".
	Identity *IdentityDatasource `json:"identity"`
	// Datasource for openstack limes.
	// Only required if Type is "limes".
	Limes *LimesDatasource `json:"limes"`
	// Datasource for openstack cinder.
	// Only required if Type is "cinder".
	Cinder *CinderDatasource `json:"cinder"`

	// How often to sync the datasource in seconds.
	SyncIntervalSeconds *int64 `json:"syncIntervalSeconds"`

	// Keystone credentials secret ref for authenticating with openstack.
	// The secret should contain the following keys:
	// - "username": The keystone username.
	// - "password": The keystone password.
	// - "userDomainName": The keystone user domain name.
	// - "projectName": The keystone project name.
	// - "projectDomainName": The keystone project domain name.
	// - "authURL": The keystone auth URL.
	KeystoneSecretRef corev1.SecretReference `json:"keystoneSecretRef"`
}

type DatasourceType string

const (
	// DatasourceTypePrometheus indicates a Prometheus datasource.
	DatasourceTypePrometheus DatasourceType = "prometheus"
	// DatasourceTypeOpenStack indicates an OpenStack datasource.
	DatasourceTypeOpenStack DatasourceType = "openstack"
)

type DatasourceSpec struct {
	// If given, configures a Prometheus datasource to fetch.
	// Type must be set to "prometheus" if this is used.
	Prometheus *PrometheusDatasource `json:"prometheus"`
	// Type must be set to "openstack" if this is used.
	// If given, configures an OpenStack datasource to fetch.
	OpenStack *OpenStackDatasource `json:"openstack,omitempty"`

	// The type of the datasource.
	Type DatasourceType `json:"type"`

	// Database credentials to use for the datasource.
	// The secret should contain the following keys:
	// - "username": The database username.
	// - "password": The database password.
	// - "host": The database host.
	// - "port": The database port.
	// - "database": The database name.
	DatabaseSecretRef corev1.SecretReference `json:"databaseSecretRef"`

	// Kubernetes secret ref for an optional sso certificate to access the host.
	// The secret should contain two keys: "cert" and "key".
	SSOSecretRef *corev1.SecretReference `json:"ssoSecretRef,omitempty"`
}

type DatasourceStatus struct {
	// When the datasource was last successfully synced.
	LastSynced metav1.Time `json:"lastSynced,omitempty"`
	// The number of rows currently stored for this datasource.
	RowCount int64 `json:"rowCount,omitempty"`
	// The time it took to perform the last sync.
	LastSyncDurationSeconds int64 `json:"lastSyncDurationSeconds,omitempty"`
	// Planned time for the next sync.
	NextSyncTime metav1.Time `json:"nextSyncTime,omitempty"`

	// If there was an error during the last sync, it is recorded here.
	Error string `json:"error,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Created",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Last Synced",type="date",JSONPath=".status.lastSynced"
// +kubebuilder:printcolumn:name="Row Count",type="integer",JSONPath=".status.rowCount"
// +kubebuilder:printcolumn:name="Error",type="string",JSONPath=".status.error"

// Datasource is the Schema for the datasources API
type Datasource struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Datasource
	// +required
	Spec DatasourceSpec `json:"spec"`

	// status defines the observed state of Datasource
	// +optional
	Status DatasourceStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// DatasourceList contains a list of Datasource
type DatasourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Datasource `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Datasource{}, &DatasourceList{})
}
