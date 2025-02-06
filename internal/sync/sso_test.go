// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sync

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
)

func TestNewHttpClient(t *testing.T) {
	tests := []struct {
		name       string
		conf       conf.SSOConfig
		wantError  bool
		selfSigned bool
	}{
		{
			name: "NoCertProvided",
			conf: conf.SSOConfig{
				Cert:       "",
				CertKey:    "",
				SelfSigned: false,
			},
			wantError: false,
		},
		{
			name: "CertProvidedNoKey",
			conf: conf.SSOConfig{
				Cert:       "dummy-cert",
				CertKey:    "",
				SelfSigned: false,
			},
			wantError: true,
		},
		{
			name: "CertAndKeyProvided",
			conf: conf.SSOConfig{
				Cert:       "dummy-cert",
				CertKey:    "dummy-key",
				SelfSigned: false,
			},
			wantError: true, // No valid cert/key pair
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewHttpClient(tt.conf); (err != nil) != tt.wantError {
				t.Errorf("NewHttpClient() error = %v, wantError %v", err, tt.wantError)
				return
			}
		})
	}
}
