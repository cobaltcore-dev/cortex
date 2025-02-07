// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/prometheus/client_golang/prometheus"
)

type AuthRequest struct {
	Auth Auth `json:"auth"`
}

type Auth struct {
	Identity Identity `json:"identity"`
	Scope    Scope    `json:"scope"`
}

type Identity struct {
	Methods  []string `json:"methods"`
	Password Password `json:"password"`
}

type Password struct {
	User User `json:"user"`
}

type User struct {
	Name     string `json:"name"`
	Domain   Domain `json:"domain"`
	Password string `json:"password"`
}

type Domain struct {
	Name string `json:"name"`
}

type Scope struct {
	Project Project `json:"project"`
}

type Project struct {
	Name   string `json:"name"`
	Domain Domain `json:"domain"`
}

type AuthResponse struct {
	TokenMetadata AuthTokenMetadata `json:"token"`
}

type AuthTokenMetadata struct {
	Catalog []Service `json:"catalog"`
}

type Service struct {
	Name      string     `json:"name"`
	Type      string     `json:"type"`
	Endpoints []Endpoint `json:"endpoints"`
}

type Endpoint struct {
	URL string `json:"url"`
}

type KeystoneAuth struct {
	nova  Endpoint
	token string // From the response header X-Subject-Token
}

type KeystoneAPI interface {
	Authenticate() (*KeystoneAuth, error)
}

type keystoneAPI struct {
	conf    conf.SyncOpenStackConfig
	monitor sync.Monitor
}

func NewKeystoneAPI(conf conf.SyncOpenStackConfig, monitor sync.Monitor) KeystoneAPI {
	return &keystoneAPI{
		conf:    conf,
		monitor: monitor,
	}
}

// Authenticate authenticates against the OpenStack Identity service (Keystone).
// This uses the configured OpenStack credentials to obtain an authentication token.
// We also extract URLs to the required services (e.g. Nova) from the response.
func (k *keystoneAPI) Authenticate() (*KeystoneAuth, error) {
	if k.monitor.PipelineRequestTimer != nil {
		hist := k.monitor.PipelineRequestTimer.WithLabelValues("openstack_keystone")
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	authRequest := AuthRequest{
		Auth: Auth{
			Identity: Identity{
				Methods: []string{"password"},
				Password: Password{
					User: User{
						Name:     k.conf.OSUsername,
						Domain:   Domain{Name: k.conf.OSUserDomainName},
						Password: k.conf.OSPassword,
					},
				},
			},
			Scope: Scope{
				Project: Project{
					Name:   k.conf.OSProjectName,
					Domain: Domain{Name: k.conf.OSProjectDomainName},
				},
			},
		},
	}

	authRequestBody, err := json.Marshal(authRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal auth request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		context.Background(), http.MethodPost,
		k.conf.KeystoneURL+"/auth/tokens", bytes.NewBuffer(authRequestBody),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client, err := sync.NewHTTPClient(k.conf.SSO)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to authenticate: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("failed to authenticate, status code: %d", resp.StatusCode)
	}

	var authResponse AuthResponse
	err = json.NewDecoder(resp.Body).Decode(&authResponse)
	if err != nil {
		return nil, fmt.Errorf("failed to decode auth response: %w", err)
	}

	// Find the Nova endpoint
	var novaEndpoint string
	for _, service := range authResponse.TokenMetadata.Catalog {
		if service.Type == "compute" {
			for _, endpoint := range service.Endpoints {
				// Skip endpoints that contain svc.kubernetes - those are
				// not reachable from outside the cluster.
				if strings.Contains(endpoint.URL, "svc.kubernetes") {
					continue
				}
				novaEndpoint = endpoint.URL
				break
			}
		}
	}
	if novaEndpoint == "" {
		return nil, errors.New("failed to find Nova endpoint")
	}

	if k.monitor.PipelineRequestProcessedCounter != nil {
		k.monitor.PipelineRequestProcessedCounter.WithLabelValues("openstack_keystone").Inc()
	}
	return &KeystoneAuth{
		nova:  Endpoint{URL: novaEndpoint},
		token: resp.Header.Get("X-Subject-Token"),
	}, nil
}
