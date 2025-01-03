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

type OpenStackAuthRequest struct {
	Auth OpenStackAuth `json:"auth"`
}

type OpenStackAuth struct {
	Identity OpenStackIdentity `json:"identity"`
	Scope    OpenStackScope    `json:"scope"`
}

type OpenStackIdentity struct {
	Methods  []string          `json:"methods"`
	Password OpenStackPassword `json:"password"`
}

type OpenStackPassword struct {
	User OpenStackUser `json:"user"`
}

type OpenStackUser struct {
	Name     string          `json:"name"`
	Domain   OpenStackDomain `json:"domain"`
	Password string          `json:"password"`
}

type OpenStackDomain struct {
	Name string `json:"name"`
}

type OpenStackScope struct {
	Project OpenStackProject `json:"project"`
}

type OpenStackProject struct {
	Name   string          `json:"name"`
	Domain OpenStackDomain `json:"domain"`
}

type OpenStackAuthResponse struct {
	TokenMetadata OpenStackAuthTokenMetadata `json:"token"`
}

type OpenStackAuthTokenMetadata struct {
	Catalog []OpenStackService `json:"catalog"`
}

type OpenStackService struct {
	Name      string              `json:"name"`
	Type      string              `json:"type"`
	Endpoints []OpenStackEndpoint `json:"endpoints"`
}

type OpenStackEndpoint struct {
	URL string `json:"url"`
}

type OpenStackKeystoneAuth struct {
	nova  OpenStackEndpoint
	token string // From the response header X-Subject-Token
}

// Authenticate authenticates against the OpenStack Identity service and returns the Nova endpoint.
func GetKeystoneAuth() (OpenStackKeystoneAuth, error) {
	authURL := mustGetEnv("OS_AUTH_URL")
	username := mustGetEnv("OS_USERNAME")
	password := mustGetEnv("OS_PASSWORD")
	projectName := mustGetEnv("OS_PROJECT_NAME")
	userDomainName := mustGetEnv("OS_USER_DOMAIN_NAME")
	projectDomainName := mustGetEnv("OS_PROJECT_DOMAIN_NAME")

	authRequest := OpenStackAuthRequest{
		Auth: OpenStackAuth{
			Identity: OpenStackIdentity{
				Methods: []string{"password"},
				Password: OpenStackPassword{
					User: OpenStackUser{
						Name:     username,
						Domain:   OpenStackDomain{Name: userDomainName},
						Password: password,
					},
				},
			},
			Scope: OpenStackScope{
				Project: OpenStackProject{
					Name:   projectName,
					Domain: OpenStackDomain{Name: projectDomainName},
				},
			},
		},
	}

	authRequestBody, err := json.Marshal(authRequest)
	if err != nil {
		return OpenStackKeystoneAuth{}, fmt.Errorf("failed to marshal auth request: %w", err)
	}

	req, err := http.NewRequest("POST", authURL+"/auth/tokens", bytes.NewBuffer(authRequestBody))
	if err != nil {
		return OpenStackKeystoneAuth{}, fmt.Errorf("failed to create auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return OpenStackKeystoneAuth{}, fmt.Errorf("failed to authenticate: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return OpenStackKeystoneAuth{}, fmt.Errorf("failed to authenticate, status code: %d", resp.StatusCode)
	}

	var authResponse OpenStackAuthResponse
	err = json.NewDecoder(resp.Body).Decode(&authResponse)
	if err != nil {
		return OpenStackKeystoneAuth{}, fmt.Errorf("failed to decode auth response: %w", err)
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
		return OpenStackKeystoneAuth{}, fmt.Errorf("nova endpoint not found")
	}

	return OpenStackKeystoneAuth{
		nova:  OpenStackEndpoint{URL: novaEndpoint},
		token: resp.Header.Get("X-Subject-Token"),
	}, nil
}
