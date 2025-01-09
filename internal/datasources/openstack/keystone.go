// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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

// Authenticate authenticates against the OpenStack Identity service and returns the Nova endpoint.
func getKeystoneAuth(conf OpenStackSyncConfig) (openStackKeystoneAuth, error) {
	authRequest := openStackAuthRequest{
		Auth: openStackAuth{
			Identity: openStackIdentity{
				Methods: []string{"password"},
				Password: openStackPassword{
					User: openStackUser{
						Name:     conf.OSUsername,
						Domain:   openStackDomain{Name: conf.OSUserDomainName},
						Password: conf.OSPassword,
					},
				},
			},
			Scope: openStackScope{
				Project: openStackProject{
					Name:   conf.OSProjectName,
					Domain: openStackDomain{Name: conf.OSProjectDomainName},
				},
			},
		},
	}

	authRequestBody, err := json.Marshal(authRequest)
	if err != nil {
		return openStackKeystoneAuth{}, fmt.Errorf("failed to marshal auth request: %w", err)
	}

	req, err := http.NewRequest("POST", conf.OSAuthUrl+"/auth/tokens", bytes.NewBuffer(authRequestBody))
	if err != nil {
		return openStackKeystoneAuth{}, fmt.Errorf("failed to create auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openStackKeystoneAuth{}, fmt.Errorf("failed to authenticate: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return openStackKeystoneAuth{}, fmt.Errorf("failed to authenticate, status code: %d", resp.StatusCode)
	}

	var authResponse openStackAuthResponse
	err = json.NewDecoder(resp.Body).Decode(&authResponse)
	if err != nil {
		return openStackKeystoneAuth{}, fmt.Errorf("failed to decode auth response: %w", err)
	}

	// Find the Nova endpoint
	var novaEndpoint string
	for _, service := range authResponse.TokenMetadata.Catalog {
		if service.Type == "compute" {
			for _, endpoint := range service.Endpoints {
				// Skip endpoints that contain svc.kubernetes - those are not reachable from outside the cluster.
				if strings.Contains(endpoint.URL, "svc.kubernetes") {
					continue
				}
				novaEndpoint = endpoint.URL
				break
			}
		}
	}
	if novaEndpoint == "" {
		return openStackKeystoneAuth{}, fmt.Errorf("nova endpoint not found")
	}

	return openStackKeystoneAuth{
		nova:  openStackEndpoint{URL: novaEndpoint},
		token: resp.Header.Get("X-Subject-Token"),
	}, nil
}
