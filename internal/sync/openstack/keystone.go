// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

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

type KeystoneAuth struct {
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
	if k.monitor.PipelineRequestProcessedCounter != nil {
		k.monitor.PipelineRequestProcessedCounter.WithLabelValues("openstack_keystone").Inc()
	}
	return &KeystoneAuth{token: resp.Header.Get("X-Subject-Token")}, nil
}
