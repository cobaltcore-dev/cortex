// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"sort"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Config aggregates configuration for all commitments components.
// Each controller and the API have their own sub-struct so that unrelated
// fields are never visible to the wrong component.
type Config struct {
	ReservationController       ReservationControllerConfig       `json:"committedResourceReservationController"`
	CommittedResourceController CommittedResourceControllerConfig `json:"committedResourceController"`
	UsageReconciler             UsageReconcilerConfig             `json:"committedResourceUsageReconciler"`
	API                         APIConfig                         `json:"committedResourceAPI"`

	// DatasourceName is the name of the Datasource CRD that provides database
	// connection info. Used to construct the UsageDBClient for report-usage and usage reconciler.
	DatasourceName string `json:"datasourceName,omitempty"`
}

// UsageReconcilerConfig holds tuning knobs for the usage reconciler.
type UsageReconcilerConfig struct {
	// CooldownInterval is the minimum time between usage reconcile runs for the same CommittedResource.
	// If a reconcile ran within this window, the next trigger is deferred until the window expires.
	// This interval also acts as the periodic fallback: every successful reconcile schedules the
	// next run after this duration so that changes not caught by watches are still picked up.
	CooldownInterval metav1.Duration `json:"cooldownInterval"`
}

func DefaultUsageReconcilerConfig() UsageReconcilerConfig {
	return UsageReconcilerConfig{
		CooldownInterval: metav1.Duration{Duration: 5 * time.Minute},
	}
}

// ApplyDefaults fills in zero-value fields from the defaults, leaving explicitly configured values intact.
func (c *UsageReconcilerConfig) ApplyDefaults() {
	d := DefaultUsageReconcilerConfig()
	if c.CooldownInterval.Duration == 0 {
		c.CooldownInterval = d.CooldownInterval
	}
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
	// RequeueIntervalRetry is the base back-off interval when placement fails (AllowRejection=false path).
	// The actual delay doubles with each consecutive failure: base * 2^min(failures, 6), capped at MaxRequeueInterval.
	// If zero (unconfigured), backoff is disabled and the controller retries immediately on every failure.
	RequeueIntervalRetry metav1.Duration `json:"requeueIntervalRetry"`

	// MaxRequeueInterval caps the exponential backoff delay.
	// Once this ceiling is reached, every subsequent retry fires after exactly this interval.
	MaxRequeueInterval metav1.Duration `json:"maxRequeueInterval"`
}

func DefaultCommittedResourceControllerConfig() CommittedResourceControllerConfig {
	return CommittedResourceControllerConfig{
		RequeueIntervalRetry: metav1.Duration{Duration: 30 * time.Second},
		MaxRequeueInterval:   metav1.Duration{Duration: 30 * time.Minute},
	}
}

// ApplyDefaults fills in zero-value fields from the defaults, leaving explicitly configured values intact.
func (c *CommittedResourceControllerConfig) ApplyDefaults() {
	d := DefaultCommittedResourceControllerConfig()
	if c.RequeueIntervalRetry.Duration == 0 {
		c.RequeueIntervalRetry = d.RequeueIntervalRetry
	}
	if c.MaxRequeueInterval.Duration == 0 {
		c.MaxRequeueInterval = d.MaxRequeueInterval
	}
}

// ResourceTypeConfig holds per-resource flags for a single resource type within a flavor group.
type ResourceTypeConfig struct {
	HandlesCommitments bool `json:"handlesCommitments"`
	HandlesDryRun      bool `json:"handlesDryRun"`
	HasCapacity        bool `json:"hasCapacity"`
	HasQuota           bool `json:"hasQuota"`
}

// RAMResourceTypeConfig extends ResourceTypeConfig with RAM-specific unit configuration.
type RAMResourceTypeConfig struct {
	HandlesCommitments bool `json:"handlesCommitments"`
	HandlesDryRun      bool `json:"handlesDryRun"`
	HasCapacity        bool `json:"hasCapacity"`
	HasQuota           bool `json:"hasQuota"`
	// RAMUnitGiB is the size of one declared LIQUID RAM unit in GiB.
	// Fixed-ratio groups set this to the smallest flavor's RAM (e.g. 480 for a 480 GiB HANA slot).
	// Variable-ratio groups set this to 1 (1 unit = 1 GiB).
	// Defaults to 1 if unset.
	RAMUnitGiB uint64 `json:"ramUnitGiB,omitempty"`
}

// RAMUnitMiB returns the RAM unit in MiB (RAMUnitGiB × 1024), defaulting to 1024 if unset.
func (c RAMResourceTypeConfig) RAMUnitMiB() uint64 {
	if c.RAMUnitGiB > 0 {
		return c.RAMUnitGiB * 1024
	}
	return 1024
}

// DeclaredUnitsToGiB converts a value in declared LIQUID units to GiB.
func (c RAMResourceTypeConfig) DeclaredUnitsToGiB(units int64) int64 {
	return units * int64(c.RAMUnitMiB()) / 1024 //nolint:gosec
}

// GiBToDeclaredUnits converts a GiB value to declared LIQUID units.
func (c RAMResourceTypeConfig) GiBToDeclaredUnits(gib int64) int64 {
	return gib * 1024 / int64(c.RAMUnitMiB()) //nolint:gosec
}

// FlavorGroupResourcesConfig groups resource type configs for the three resources of a flavor group.
type FlavorGroupResourcesConfig struct {
	RAM       RAMResourceTypeConfig `json:"ram"`
	Cores     ResourceTypeConfig    `json:"cores"`
	Instances ResourceTypeConfig    `json:"instances"`
}

// LogFlavorGroupResourceConfig logs the effective RAM unit for each configured flavor group.
// It warns when RAMUnitGiB is 0, which silently defaults to 1 GiB.
func LogFlavorGroupResourceConfig(log logr.Logger, cfg map[string]FlavorGroupResourcesConfig) {
	if len(cfg) == 0 {
		log.Info("WARNING: no flavorGroupResourceConfig set; all flavor groups will use default RAM unit of 1 GiB")
		return
	}
	groups := make([]string, 0, len(cfg))
	for g := range cfg {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	for _, groupName := range groups {
		resCfg := cfg[groupName]
		if resCfg.RAM.RAMUnitGiB == 0 {
			log.Info("WARNING: ramUnitGiB not configured for flavor group, defaulting to 1 GiB",
				"flavorGroup", groupName)
		} else {
			log.Info("flavor group RAM unit",
				"flavorGroup", groupName, "ramUnitGiB", resCfg.RAM.RAMUnitGiB)
		}
	}
}

// ResourceConfigForGroup returns the resource config for the given flavor group name,
// falling back to the "*" catch-all if no exact match exists.
func ResourceConfigForGroup(cfg map[string]FlavorGroupResourcesConfig, groupName string) FlavorGroupResourcesConfig {
	if cfg != nil {
		if c, ok := cfg[groupName]; ok {
			return c
		}
		if c, ok := cfg["*"]; ok {
			return c
		}
	}
	return FlavorGroupResourcesConfig{}
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
	// EnableQuotaAPI controls whether the quota API endpoint is active.
	// When false, the endpoint will return HTTP 503 Service Unavailable.
	EnableQuotaAPI bool `json:"enableQuota"`
	// WatchTimeout is how long the change-commitments handler polls CommittedResource
	// CRD conditions before giving up and rolling back.
	WatchTimeout metav1.Duration `json:"watchTimeout"`
	// WatchPollInterval is how frequently the change-commitments handler polls
	// CommittedResource CRD conditions while waiting for the controller outcome.
	WatchPollInterval metav1.Duration `json:"watchPollInterval"`
	// FlavorGroupResourceConfig maps flavor group IDs to resource flag configs; "*" acts as catch-all.
	FlavorGroupResourceConfig map[string]FlavorGroupResourcesConfig `json:"flavorGroupResourceConfig,omitempty"`
	// QuotaServedAvailabilityZones restricts quota handling to these AZs.
	// Quota received for AZs not in this list is silently skipped.
	// If empty/nil, no pre-filtering is applied (relies on error-based fallback).
	QuotaServedAvailabilityZones []string `json:"quotaServedAvailabilityZones,omitempty"`
}

// ResourceConfigForGroup returns the resource config for the given flavor group name,
// falling back to the "*" catch-all if no exact match exists.
func (c APIConfig) ResourceConfigForGroup(groupName string) FlavorGroupResourcesConfig {
	return ResourceConfigForGroup(c.FlavorGroupResourceConfig, groupName)
}

func DefaultAPIConfig() APIConfig {
	return APIConfig{
		EnableChangeCommitments: true,
		EnableReportUsage:       true,
		EnableReportCapacity:    true,
		EnableQuotaAPI:          true,
		WatchTimeout:            metav1.Duration{Duration: 10 * time.Second},
		WatchPollInterval:       metav1.Duration{Duration: 500 * time.Millisecond},
	}
}
