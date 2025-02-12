// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sync

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/cobaltcore-dev/cortex/internal/conf"
)

// Create a new HTTP client with the given SSO configuration.
func NewHTTPClient(conf conf.SSOConfig) (*http.Client, error) {
	if conf.Cert == "" {
		// Disable SSO if no certificate is provided.
		slog.Info("making http requests without SSO")
		return &http.Client{}, nil
	}
	// If we have a public key, we also need a private key.
	if conf.CertKey == "" {
		return nil, errors.New("missing cert key for SSO")
	}
	cert, err := tls.X509KeyPair(
		[]byte(conf.Cert),
		[]byte(conf.CertKey),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load client certificate: %w", err)
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AddCert(cert.Leaf)
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			RootCAs:      caCertPool,
			// If the cert is self signed, skip verification.
			//nolint:gosec
			InsecureSkipVerify: conf.SelfSigned,
		},
	}}, nil
}
