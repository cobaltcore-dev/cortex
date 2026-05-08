// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package sso

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Configuration for single-sign-on (SSO).
type SSOConfig struct {
	Cert    string `json:"cert,omitempty"`
	CertKey string `json:"certKey,omitempty"`

	// If the certificate is self-signed, we need to skip verification.
	SelfSigned bool `json:"selfSigned,omitempty"`
}

// Custom HTTP round tripper that logs each request.
type requestLogger struct {
	T http.RoundTripper
}

// RoundTrip logs the request URL before making the request.
func (lrt *requestLogger) RoundTrip(req *http.Request) (*http.Response, error) {
	slog.Info("making http request", "url", req.URL.String())
	return lrt.T.RoundTrip(req)
}

// Kubernetes connector which initializes the sso connection from a secret.
type Connector struct{ client.Client }

// Create a new http client with SSO configuration given in a kubernetes secret.
func (c Connector) FromSecretRef(ctx context.Context, ref corev1.SecretReference) (*http.Client, error) {
	authSecret := &corev1.Secret{}
	if err := c.Get(ctx, client.ObjectKey{
		Namespace: ref.Namespace,
		Name:      ref.Name,
	}, authSecret); err != nil {
		return nil, err
	}
	cert, ok := authSecret.Data["cert"]
	if !ok {
		return nil, errors.New("missing 'cert' in SSO secret")
	}
	key, ok := authSecret.Data["key"]
	if !ok {
		return nil, errors.New("missing 'key' in SSO secret")
	}
	selfSigned := false
	if val, ok := authSecret.Data["selfSigned"]; ok {
		if string(val) == "true" {
			selfSigned = true
		}
	}
	conf := SSOConfig{
		Cert:       string(cert),
		CertKey:    string(key),
		SelfSigned: selfSigned,
	}
	return NewHTTPClient(conf)
}

// NewTransport returns an *http.Transport configured with TLS client
// certificates from the given SSO config. If no certificate is provided,
// a plain *http.Transport is returned.
func NewTransport(conf SSOConfig) (*http.Transport, error) {
	if conf.Cert == "" {
		return &http.Transport{}, nil
	}
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
	return &http.Transport{
		TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			RootCAs:      caCertPool,
			// If the cert is self signed, skip verification.
			//nolint:gosec
			InsecureSkipVerify: conf.SelfSigned,
		},
	}, nil
}

// Create a new HTTP client with the given SSO configuration
// and logging for each request.
func NewHTTPClient(conf SSOConfig) (*http.Client, error) {
	transport, err := NewTransport(conf)
	if err != nil {
		return nil, err
	}
	if conf.Cert == "" {
		slog.Debug("making http requests without SSO")
	}
	return &http.Client{Transport: &requestLogger{T: transport}}, nil
}
