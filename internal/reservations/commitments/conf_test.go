// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestConfig_Structure(t *testing.T) {
	// Test that Config struct can be instantiated
	config := Config{
		KeystoneSecretRef: corev1.SecretReference{
			Name:      "keystone-secret",
			Namespace: "default",
		},
		SSOSecretRef: &corev1.SecretReference{
			Name:      "sso-secret",
			Namespace: "default",
		},
	}

	if config.KeystoneSecretRef.Name != "keystone-secret" {
		t.Errorf("Expected keystone-secret, got %v", config.KeystoneSecretRef.Name)
	}
	if config.SSOSecretRef.Name != "sso-secret" {
		t.Errorf("Expected sso-secret, got %v", config.SSOSecretRef.Name)
	}
}
