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
	ReservationController       ReservationControllerConfig       `json:"reservationController"`
	CommittedResourceController CommittedResourceControllerConfig `json:"committedResourceController"`
	API                         APIConfig                         `json:"api"`

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
}

// ApplyDefaults fills in any unset values with defaults.
func (c *Config) ApplyDefaults() {
	c.ReservationController.applyDefaults()
	c.CommittedResourceController.applyDefaults()
	// APIConfig booleans: false is a valid operator choice, so no defaults applied.
	if c.API.WatchTimeout.Duration == 0 {
		c.API.WatchTimeout = DefaultAPIConfig().WatchTimeout
	}
	if c.API.WatchPollInterval.Duration == 0 {
		c.API.WatchPollInterval = DefaultAPIConfig().WatchPollInterval
	}
}

func (c *ReservationControllerConfig) applyDefaults() {
	d := DefaultReservationControllerConfig()
	if c.RequeueIntervalActive.Duration == 0 {
		c.RequeueIntervalActive = d.RequeueIntervalActive
	}
	if c.RequeueIntervalRetry.Duration == 0 {
		c.RequeueIntervalRetry = d.RequeueIntervalRetry
	}
	if c.RequeueIntervalGracePeriod.Duration == 0 {
		c.RequeueIntervalGracePeriod = d.RequeueIntervalGracePeriod
	}
	if c.AllocationGracePeriod.Duration == 0 {
		c.AllocationGracePeriod = d.AllocationGracePeriod
	}
	if c.SchedulerURL == "" {
		c.SchedulerURL = d.SchedulerURL
	}
	if c.PipelineDefault == "" {
		c.PipelineDefault = d.PipelineDefault
	}
}

func (c *CommittedResourceControllerConfig) applyDefaults() {
	d := DefaultCommittedResourceControllerConfig()
	if c.RequeueIntervalRetry.Duration == 0 {
		c.RequeueIntervalRetry = d.RequeueIntervalRetry
	}
}

func DefaultConfig() Config {
	return Config{
		ReservationController:       DefaultReservationControllerConfig(),
		CommittedResourceController: DefaultCommittedResourceControllerConfig(),
		API:                         DefaultAPIConfig(),
	}
}

func DefaultReservationControllerConfig() ReservationControllerConfig {
	return ReservationControllerConfig{
		RequeueIntervalActive:      metav1.Duration{Duration: 5 * time.Minute},
		RequeueIntervalRetry:       metav1.Duration{Duration: 1 * time.Minute},
		RequeueIntervalGracePeriod: metav1.Duration{Duration: 1 * time.Minute},
		AllocationGracePeriod:      metav1.Duration{Duration: 15 * time.Minute},
		PipelineDefault:            "kvm-general-purpose-load-balancing",
		SchedulerURL:               "http://localhost:8080/scheduler/nova/external",
	}
}

func DefaultCommittedResourceControllerConfig() CommittedResourceControllerConfig {
	return CommittedResourceControllerConfig{
		RequeueIntervalRetry: metav1.Duration{Duration: 1 * time.Minute},
	}
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
