// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Config aggregates configuration for all commitments components.
// Each controller and the API have their own sub-struct so that unrelated
// fields are never visible to the wrong component.
type Config struct {
	ReservationController       ReservationControllerConfig       `json:"committedResourceReservationController"`
	CommittedResourceController CommittedResourceControllerConfig `json:"committedResourceController"`
	API                         APIConfig                         `json:"committedResourceAPI"`

	// DatasourceName is the name of the Datasource CRD that provides database
	// connection info. Used to construct the UsageDBClient for report-usage.
	DatasourceName string `json:"datasourceName,omitempty"`
}

// ReservationControllerConfig holds tuning knobs for the Reservation CRD controller.
type ReservationControllerConfig struct {
	// RequeueIntervalActive is how often to re-verify a healthy Reservation CRD.
	RequeueIntervalActive metav1.Duration `json:"requeueIntervalActive"`
	// RequeueIntervalRetry is the back-off interval when knowledge is unavailable.
	RequeueIntervalRetry metav1.Duration `json:"requeueIntervalRetry"`
	// RequeueIntervalGracePeriod is how often to re-check while a VM allocation
	// is still within AllocationGracePeriod. Shorter than RequeueIntervalActive.
	RequeueIntervalGracePeriod metav1.Duration `json:"requeueIntervalGracePeriod"`
	// AllocationGracePeriod is the time window after a VM is allocated to a
	// reservation during which it's expected to appear on the target host.
	// VMs not confirmed within this period are considered stale and removed.
	AllocationGracePeriod metav1.Duration `json:"allocationGracePeriod"`
	// SchedulerURL is the endpoint of the nova external scheduler.
	SchedulerURL string `json:"schedulerURL"`
	// PipelineDefault is the fallback pipeline when no FlavorGroupPipelines entry matches.
	PipelineDefault string `json:"pipelineDefault"`
	// FlavorGroupPipelines maps flavor group IDs to pipeline names; "*" acts as catch-all.
	FlavorGroupPipelines map[string]string `json:"flavorGroupPipelines,omitempty"`
}

// CommittedResourceControllerConfig holds tuning knobs for the CommittedResource CRD controller.
type CommittedResourceControllerConfig struct {
	// RequeueIntervalRetry is the back-off interval when placement is pending or failed.
	RequeueIntervalRetry metav1.Duration `json:"requeueIntervalRetry"`
}

// ResourceTypeConfig holds per-resource flags for a single resource type within a flavor group.
type ResourceTypeConfig struct {
	HandlesCommitments bool `json:"handlesCommitments"`
	HasCapacity        bool `json:"hasCapacity"`
	HasQuota           bool `json:"hasQuota"`
}

// FlavorGroupResourcesConfig groups resource type configs for the three resources of a flavor group.
type FlavorGroupResourcesConfig struct {
	RAM       ResourceTypeConfig `json:"ram"`
	Cores     ResourceTypeConfig `json:"cores"`
	Instances ResourceTypeConfig `json:"instances"`
}

// APIConfig holds configuration for the LIQUID commitment HTTP endpoints.
type APIConfig struct {
	// EnableChangeCommitments controls whether the change-commitments endpoint is active.
	// When false the endpoint returns HTTP 503; the info endpoint remains available.
	EnableChangeCommitments bool `json:"enableChangeCommitments"`
	// EnableReportUsage controls whether the report-usage endpoint is active.
	EnableReportUsage bool `json:"enableReportUsage"`
	// EnableReportCapacity controls whether the report-capacity endpoint is active.
	EnableReportCapacity bool `json:"enableReportCapacity"`
	// WatchTimeout is how long the change-commitments handler polls CommittedResource
	// CRD conditions before giving up and rolling back.
	WatchTimeout metav1.Duration `json:"watchTimeout"`
	// WatchPollInterval is how frequently the change-commitments handler polls
	// CommittedResource CRD conditions while waiting for the controller outcome.
	WatchPollInterval metav1.Duration `json:"watchPollInterval"`
	// FlavorGroupResourceConfig maps flavor group IDs to resource flag configs; "*" acts as catch-all.
	FlavorGroupResourceConfig map[string]FlavorGroupResourcesConfig `json:"flavorGroupResourceConfig,omitempty"`
}

// ResourceConfigForGroup returns the resource config for the given flavor group ID,
// falling back to the "*" catch-all if no exact match exists.
func (c APIConfig) ResourceConfigForGroup(groupID string) FlavorGroupResourcesConfig {
	if c.FlavorGroupResourceConfig != nil {
		if cfg, ok := c.FlavorGroupResourceConfig[groupID]; ok {
			return cfg
		}
		if cfg, ok := c.FlavorGroupResourceConfig["*"]; ok {
			return cfg
		}
	}
	return FlavorGroupResourcesConfig{}
}

func DefaultAPIConfig() APIConfig {
	return APIConfig{
		EnableChangeCommitments: true,
		EnableReportUsage:       true,
		EnableReportCapacity:    true,
		WatchTimeout:            metav1.Duration{Duration: 10 * time.Second},
		WatchPollInterval:       metav1.Duration{Duration: 500 * time.Millisecond},
	}
}
