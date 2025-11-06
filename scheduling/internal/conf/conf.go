// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	corev1 "k8s.io/api/core/v1"
)

type Config struct {
	// The operator will only touch CRs with this operator name.
	Operator string `json:"operator"`

	// Whether to disable dry-run for descheduler steps.
	DisableDeschedulerDryRun bool `json:"disableDeschedulerDryRun"`

	// Secret ref to keystone credentials stored in a k8s secret.
	KeystoneSecretRef corev1.SecretReference `json:"keystoneSecretRef"`

	// Secret ref to SSO credentials stored in a k8s secret, if applicable.
	SSOSecretRef *corev1.SecretReference `json:"ssoSecretRef"`

	// List of enabled controllers.
	EnabledControllers []string `json:"enabledControllers"`
}
