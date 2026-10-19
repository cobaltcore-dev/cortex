// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package rescheduler

// DefaultSchedulerURL is the external scheduler endpoint used for initial
// placement when none is configured.
const DefaultSchedulerURL = "http://localhost:8080/scheduler/nova/external"

// Config configures the rescheduler.
type Config struct {
	// SchedulerURL is the endpoint of the external scheduler used for initial
	// placement over HTTP. When empty, DefaultSchedulerURL is used.
	SchedulerURL string `json:"reschedulerSchedulerURL,omitempty"`
}
