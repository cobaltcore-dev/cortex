// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package quota

import (
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// QuotaControllerConfig defines the configuration for the quota controller.
type QuotaControllerConfig struct {
	// FullReconcileInterval is the periodic full reconcile interval.
	// Full reconcile re-reads all VMs from Postgres and recomputes all usage. Default: 5m.
	FullReconcileInterval metav1.Duration `json:"fullReconcileInterval"`

	// CRStateFilter defines which CommittedResource states to include
	// when summing cr_actual_usage. Default: ["confirmed", "guaranteed"]
	CRStateFilter []v1alpha1.CommitmentStatus `json:"crStateFilter"`

	// EnableHVDiff enables incremental TotalUsage updates via HV instance diffs.
	// When false, only periodic full reconciles update TotalUsage (safer but slower convergence).
	// Default: true.
	EnableHVDiff *bool `json:"enableHVDiff,omitempty"`
}

// ApplyDefaults fills in any unset values with defaults.
func (c *QuotaControllerConfig) ApplyDefaults() {
	defaults := DefaultQuotaControllerConfig()
	if c.FullReconcileInterval.Duration == 0 {
		c.FullReconcileInterval = defaults.FullReconcileInterval
	}
	if len(c.CRStateFilter) == 0 {
		c.CRStateFilter = defaults.CRStateFilter
	}
	if c.EnableHVDiff == nil {
		c.EnableHVDiff = defaults.EnableHVDiff
	}
}

// IsHVDiffEnabled returns whether incremental HV diff is enabled.
func (c *QuotaControllerConfig) IsHVDiffEnabled() bool {
	if c.EnableHVDiff == nil {
		return true // default: enabled
	}
	return *c.EnableHVDiff
}

// DefaultQuotaControllerConfig returns a default configuration.
func DefaultQuotaControllerConfig() QuotaControllerConfig {
	enableHVDiff := true
	return QuotaControllerConfig{
		FullReconcileInterval: metav1.Duration{Duration: 5 * time.Minute},
		CRStateFilter: []v1alpha1.CommitmentStatus{
			v1alpha1.CommitmentStatusConfirmed,
			v1alpha1.CommitmentStatusGuaranteed,
		},
		EnableHVDiff: &enableHVDiff,
	}
}
