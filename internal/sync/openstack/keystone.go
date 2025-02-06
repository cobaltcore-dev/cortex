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

type openStackAuthRequest struct {
	Auth openStackAuth `json:"auth"`
}

type openStackAuth struct {
	Identity openStackIdentity `json:"identity"`
	Scope    openStackScope    `json:"scope"`
}

type openStackIdentity struct {
	Methods  []string          `json:"methods"`
	Password openStackPassword `json:"password"`
}

type openStackPassword struct {
	User openStackUser `json:"user"`
}

type openStackUser struct {
	Name     string          `json:"name"`
	Domain   openStackDomain `json:"domain"`
	Password string          `json:"password"`
}

type openStackDomain struct {
	Name string `json:"name"`
}

type openStackScope struct {
	Project openStackProject `json:"project"`
}

type openStackProject struct {
	Name   string          `json:"name"`
	Domain openStackDomain `json:"domain"`
}

type openStackAuthResponse struct {
	TokenMetadata openStackAuthTokenMetadata `json:"token"`
}

type openStackAuthTokenMetadata struct {
	Catalog []openStackService `json:"catalog"`
}

type openStackService struct {
	Name      string              `json:"name"`
	Type      string              `json:"type"`
	Endpoints []openStackEndpoint `json:"endpoints"`
}

type openStackEndpoint struct {
	URL string `json:"url"`
}

type openStackKeystoneAuth struct {
	nova  openStackEndpoint
	token string // From the response header X-Subject-Token
}

type KeystoneAPI interface {
	Authenticate() (*openStackKeystoneAuth, error)
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
func (k *keystoneAPI) Authenticate() (*openStackKeystoneAuth, error) {
	if k.monitor.PipelineRequestTimer != nil {
		hist := k.monitor.PipelineRequestTimer.WithLabelValues("openstack_keystone")
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	authRequest := openStackAuthRequest{
		Auth: openStackAuth{
			Identity: openStackIdentity{
				Methods: []string{"password"},
				Password: openStackPassword{
					User: openStackUser{
						Name:     k.conf.OSUsername,
						Domain:   openStackDomain{Name: k.conf.OSUserDomainName},
						Password: k.conf.OSPassword,
					},
				},
			},
			Scope: openStackScope{
				Project: openStackProject{
					Name:   k.conf.OSProjectName,
					Domain: openStackDomain{Name: k.conf.OSProjectDomainName},
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

	client, err := sync.NewHttpClient(k.conf.SSO)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to authenticate: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("failed to authenticate, status code: %d", resp.StatusCode)
	}

	var authResponse openStackAuthResponse
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
	return &openStackKeystoneAuth{
		nova:  openStackEndpoint{URL: novaEndpoint},
		token: resp.Header.Get("X-Subject-Token"),
	}, nil
}
