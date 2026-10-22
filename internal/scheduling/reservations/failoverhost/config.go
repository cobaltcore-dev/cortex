// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// FailoverHostConfig contains configuration for failover host reservations.
type FailoverHostConfig struct {
	// Ratio is a global ratio that determines how many failover hosts inside
	// a partition are created. For example, a ratio of 0.1 means that
	// if a partition consists of 10 hosts, 1 failover host will be created.
	// The number of resulting failover hosts in a partition will be rounded
	// down. This means if there are not sufficient hosts inside a partition,
	// it is possible that no failover hosts will be created.
	Ratio float64 `json:"ratio"`

	// Minimum is the minimum number of failover hosts that should be created
	// in a partition, regardless of the ratio. This ensures that even small
	// partitions have at least a certain number of failover hosts.
	Minimum int `json:"minimum"`

	// GenerationInterval is the interval within which failover partitions are
	// recalculated and failover host reservations are updated accordingly.
	GenerationInterval metav1.Duration `json:"generationInterval"`
}
