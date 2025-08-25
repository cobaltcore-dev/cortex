// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sso

// Configuration for single-sign-on (SSO).
type Config struct {
	Cert    string `json:"cert,omitempty"`
	CertKey string `json:"certKey,omitempty"`

	// If the certificate is self-signed, we need to skip verification.
	SelfSigned bool `json:"selfSigned,omitempty"`
}
