// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"errors"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	// Some datasources may depend on other datasources to be present.
	// If these aren't yet available, this will be the returned error.
	ErrWaitingForDependencyDatasource = errors.New("waiting for dependency datasource to become available")
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

	// Time range to query the data for.
	// +kubebuilder:default="2419200s"
	TimeRange metav1.Duration `json:"timeRange"`
	// The interval at which to query the data.
	// +kubebuilder:default="86400s"
	Interval metav1.Duration `json:"interval"`
	// The resolution of the data.
	// +kubebuilder:default="43200s"
	Resolution metav1.Duration `json:"resolution"`

	// Secret containing the following keys:
	// - "url": The prometheus URL.
	SecretRef corev1.SecretReference `json:"secretRef"`
}

type NovaDatasourceType string

const (
	NovaDatasourceTypeServers        NovaDatasourceType = "servers"
	NovaDatasourceTypeDeletedServers NovaDatasourceType = "deletedServers"
	NovaDatasourceTypeHypervisors    NovaDatasourceType = "hypervisors"
	NovaDatasourceTypeFlavors        NovaDatasourceType = "flavors"
	NovaDatasourceTypeMigrations     NovaDatasourceType = "migrations"
	NovaDatasourceTypeAggregates     NovaDatasourceType = "aggregates"
)

type NovaDatasource struct {
	// The type of resource to sync.
	Type NovaDatasourceType `json:"type"`
	// Time frame in minutes for the changes-since parameter when fetching
	// deleted servers. Set if the Type is "deletedServers".
	DeletedServersChangesSinceMinutes *int `json:"deletedServersChangesSinceMinutes,omitempty"`
}

type PlacementDatasourceType string

const (
	PlacementDatasourceTypeResourceProviders               PlacementDatasourceType = "resourceProviders"
	PlacementDatasourceTypeResourceProviderInventoryUsages PlacementDatasourceType = "resourceProviderInventoryUsages"
	PlacementDatasourceTypeResourceProviderTraits          PlacementDatasourceType = "resourceProviderTraits"
)

type PlacementDatasource struct {
	// The type of resource to sync.
	Type PlacementDatasourceType `json:"type"`
}

type ManilaDatasourceType string

const (
	ManilaDatasourceTypeStoragePools ManilaDatasourceType = "storagePools"
)

type ManilaDatasource struct {
	// The type of resource to sync.
	Type ManilaDatasourceType `json:"type"`
}

type IdentityDatasourceType string

const (
	IdentityDatasourceTypeProjects IdentityDatasourceType = "projects"
	IdentityDatasourceTypeDomains  IdentityDatasourceType = "domains"
)

type IdentityDatasource struct {
	// The type of resource to sync.
	Type IdentityDatasourceType `json:"type"`
}

type LimesDatasourceType string

const (
	LimesDatasourceTypeProjectCommitments LimesDatasourceType = "projectCommitments"
)

type LimesDatasource struct {
	// The type of resource to sync.
	Type LimesDatasourceType `json:"type"`
}

type CinderDatasourceType string

const (
	CinderDatasourceTypeStoragePools CinderDatasourceType = "storagePools"
)

type CinderDatasource struct {
	// The type of resource to sync.
	Type CinderDatasourceType `json:"type"`
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
	// +kubebuilder:validation:Optional
	Nova NovaDatasource `json:"nova"`
	// Datasource for openstack placement.
	// Only required if Type is "placement".
	// +kubebuilder:validation:Optional
	Placement PlacementDatasource `json:"placement"`
	// Datasource for openstack manila.
	// Only required if Type is "manila".
	// +kubebuilder:validation:Optional
	Manila ManilaDatasource `json:"manila"`
	// Datasource for openstack identity.
	// Only required if Type is "identity".
	// +kubebuilder:validation:Optional
	Identity IdentityDatasource `json:"identity"`
	// Datasource for openstack limes.
	// Only required if Type is "limes".
	// +kubebuilder:validation:Optional
	Limes LimesDatasource `json:"limes"`
	// Datasource for openstack cinder.
	// Only required if Type is "cinder".
	// +kubebuilder:validation:Optional
	Cinder CinderDatasource `json:"cinder"`

	// How often to sync the datasource.
	// +kubebuilder:default="60s"
	SyncInterval metav1.Duration `json:"syncInterval"`

	// Keystone credentials secret ref for authenticating with openstack.
	// The secret should contain the following keys:
	// - "availability": The service availability, e.g. "public", "internal", or "admin".
	// - "url": The keystone auth URL.
	// - "username": The keystone username.
	// - "password": The keystone password.
	// - "userDomainName": The keystone user domain name.
	// - "projectName": The keystone project name.
	// - "projectDomainName": The keystone project domain name.
	SecretRef corev1.SecretReference `json:"secretRef"`
}

type DatasourceType string

const (
	// DatasourceTypePrometheus indicates a Prometheus datasource.
	DatasourceTypePrometheus DatasourceType = "prometheus"
	// DatasourceTypeOpenStack indicates an OpenStack datasource.
	DatasourceTypeOpenStack DatasourceType = "openstack"
)

type DatasourceSpec struct {
	// SchedulingDomain defines in which scheduling domain this datasource
	// is used (e.g., nova, cinder, manila).
	SchedulingDomain SchedulingDomain `json:"schedulingDomain"`

	// If given, configures a Prometheus datasource to fetch.
	// Type must be set to "prometheus" if this is used.
	// +kubebuilder:validation:Optional
	Prometheus PrometheusDatasource `json:"prometheus"`
	// If given, configures an OpenStack datasource to fetch.
	// Type must be set to "openstack" if this is used.
	// +kubebuilder:validation:Optional
	OpenStack OpenStackDatasource `json:"openstack,omitempty"`

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
	// +kubebuilder:validation:Optional
	SSOSecretRef *corev1.SecretReference `json:"ssoSecretRef,omitempty"`
}

const (
	// The datasource is ready to be used.
	DatasourceConditionReady = "Ready"
)

type DatasourceStatus struct {
	// When the datasource was last successfully synced.
	LastSynced metav1.Time `json:"lastSynced,omitempty"`
	// The number of objects currently stored for this datasource.
	NumberOfObjects int64 `json:"numberOfObjects,omitempty"`
	// Planned time for the next sync.
	NextSyncTime metav1.Time `json:"nextSyncTime,omitempty"`

	// The current status conditions of the datasource.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Domain",type="string",JSONPath=".spec.schedulingDomain"
// +kubebuilder:printcolumn:name="Created",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Synced",type="date",JSONPath=".status.lastSynced"
// +kubebuilder:printcolumn:name="Next",type="string",JSONPath=".status.nextSyncTime"
// +kubebuilder:printcolumn:name="Objects",type="integer",JSONPath=".status.numberOfObjects"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"

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
