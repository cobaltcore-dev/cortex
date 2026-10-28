// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package rescheduler

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DefaultSchedulerURL is the external scheduler endpoint used for initial
// placement when none is configured.
const DefaultSchedulerURL = "http://localhost:8080/scheduler/nova/external"

// DefaultCandidateSaturation is the candidate-count threshold used when ordering
// entities for reservation: counts at or above it are treated as equal ("plenty
// of options"), so the ordering no longer distinguishes between them.
const DefaultCandidateSaturation = 3

// DefaultBlockedHostsTTL is how long a succeeded request keeps blocking its
// hosts before the block is released, when none is configured.
var DefaultBlockedHostsTTL = metav1.Duration{Duration: 5 * time.Minute}

// DefaultAPITimeout bounds a single apiserver read or write made by the
// scheduler, so a slow or hung call cannot wedge the single worker goroutine.
// The rest config carries no timeout by default, so this is applied explicitly.
var DefaultAPITimeout = metav1.Duration{Duration: 10 * time.Second}

// Config configures the rescheduler.
type Config struct {
	// SchedulerURL is the endpoint of the external scheduler used for initial
	// placement over HTTP. When empty, DefaultSchedulerURL is used.
	SchedulerURL string `json:"reschedulerSchedulerURL,omitempty"`

	// CandidateSaturation caps the candidate count considered when ordering
	// entities for reservation (fewest-options-first). Once an entity has this
	// many candidate targets, having more does not change its order. When zero,
	// DefaultCandidateSaturation is used.
	CandidateSaturation int `json:"reschedulerCandidateSaturation,omitempty"`

	// BlockedHostsTTL is the grace period for which a succeeded request keeps its
	// host-block after the move completes, to avoid incoming placement landing on
	// a just-drained host before the environment settles. When zero,
	// DefaultBlockedHostsTTL is used.
	BlockedHostsTTL metav1.Duration `json:"reschedulerBlockedHostsTTL,omitempty"`

	// APITimeout bounds each apiserver read and write the scheduler performs.
	// When zero, DefaultAPITimeout is used.
	APITimeout metav1.Duration `json:"reschedulerAPITimeout,omitempty"`
}
