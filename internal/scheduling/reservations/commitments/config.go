// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"time"

	corev1 "k8s.io/api/core/v1"
)

type Config struct {

	// RequeueIntervalActive is the interval for requeueing active reservations for verification.
	RequeueIntervalActive time.Duration `json:"requeueIntervalActive"`
	// RequeueIntervalRetry is the interval for requeueing when retrying after knowledge is not ready.
	RequeueIntervalRetry time.Duration `json:"requeueIntervalRetry"`
	// PipelineDefault is the default pipeline used for scheduling committed resource reservations.
	PipelineDefault string `json:"pipelineDefault"`

	// NovaExternalScheduler is the endpoint where to find the nova external scheduler endpoint.
	NovaExternalScheduler string `json:"novaExternalScheduler"`

	// Secret ref to SSO credentials stored in a k8s secret, if applicable.
	SSOSecretRef *corev1.SecretReference `json:"ssoSecretRef"`

	// Secret ref to keystone credentials stored in a k8s secret.
	KeystoneSecretRef corev1.SecretReference `json:"keystoneSecretRef"`

	// Secret ref to the database credentials for querying VM state.
	DatabaseSecretRef *corev1.SecretReference `json:"databaseSecretRef,omitempty"`

	// FlavorGroupPipelines maps flavor group names to pipeline names.
	// Example: {"2152": "kvm-commitments-hana", "2101": "kvm-commitments-gp", "*": "kvm-commitments-gp"}
	// Used to select different scheduling pipelines based on flavor group characteristics.
	FlavorGroupPipelines map[string]string `json:"flavorGroupPipelines,omitempty"`

	// API configuration

	// ChangeAPIWatchReservationsTimeout defines how long to wait for reservations to become ready before timing out and rolling back.
	ChangeAPIWatchReservationsTimeout time.Duration `json:"changeAPIWatchReservationsTimeout"`

	// ChangeAPIWatchReservationsPollInterval defines how frequently to poll reservation status during watch.
	ChangeAPIWatchReservationsPollInterval time.Duration `json:"changeAPIWatchReservationsPollInterval"`

	// EnableChangeCommitmentsAPI controls whether the change-commitments API endpoint is active.
	// When false, the endpoint will return HTTP 503 Service Unavailable.
	// The info endpoint remains available for health checks.
	EnableChangeCommitmentsAPI bool `json:"enableChangeCommitmentsAPI"`
}

func DefaultConfig() Config {
	return Config{
		RequeueIntervalActive:                  5 * time.Minute,
		RequeueIntervalRetry:                   1 * time.Minute,
		PipelineDefault:                        "kvm-committed-resource-reservation-general-purpose",
		NovaExternalScheduler:                  "http://localhost:8080/scheduler/nova/external",
		ChangeAPIWatchReservationsTimeout:      10 * time.Second,
		ChangeAPIWatchReservationsPollInterval: 500 * time.Millisecond,
		EnableChangeCommitmentsAPI:             true,
	}
}
