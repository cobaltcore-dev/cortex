// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/keystone"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/domains"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
)

type IdentityAPI interface {
	Init(ctx context.Context)
	// Retrieves all domains from the OpenStack identity service.
	GetAllDomains(ctx context.Context) ([]Domain, error)
	// Retrieves all projects from the OpenStack identity service.
	GetAllProjects(ctx context.Context) ([]Project, error)
}

type identityAPI struct {
	mon         sync.Monitor
	keystoneAPI keystone.KeystoneAPI
	sc          *gophercloud.ServiceClient
	conf        IdentityConf
}

func NewIdentityAPI(mon sync.Monitor, k keystone.KeystoneAPI, conf IdentityConf) IdentityAPI {
	return &identityAPI{mon: mon, keystoneAPI: k, conf: conf}
}

func (api *identityAPI) Init(ctx context.Context) {
	if err := api.keystoneAPI.Authenticate(ctx); err != nil {
		panic(err)
	}
	provider := api.keystoneAPI.Client()
	serviceType := "identity"
	url, err := api.keystoneAPI.FindEndpoint("public", serviceType)
	if err != nil {
		panic(err)
	}
	slog.Info("using identity endpoint", "url", url)
	api.sc = &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       url,
		Type:           serviceType,
	}
}

// Get all the domains from the OpenStack identity service.
func (api *identityAPI) GetAllDomains(ctx context.Context) ([]Domain, error) {
	slog.Info("fetching identity data", "label", "domains")
	client := api.sc
	allPages, err := domains.List(client, nil).AllPages(ctx)
	if err != nil {
		return nil, err
	}
	var data = &struct {
		Domains []Domain `json:"domains"`
	}{}
	if err := allPages.(domains.DomainPage).ExtractInto(data); err != nil {
		return nil, err
	}
	slog.Info("fetched identity data", "label", "domains", "count", len(data.Domains))
	return data.Domains, nil
}

// Get all the projects from the OpenStack identity service.
func (api *identityAPI) GetAllProjects(ctx context.Context) ([]Project, error) {
	slog.Info("fetching identity data", "label", "projects")
	client := api.sc
	allPages, err := projects.List(client, nil).AllPages(ctx)
	if err != nil {
		return nil, err
	}
	var data = &struct {
		Projects []Project `json:"projects"`
	}{}
	if err := allPages.(projects.ProjectPage).ExtractInto(data); err != nil {
		return nil, err
	}
	slog.Info("fetched identity data", "label", "projects", "count", len(data.Projects))
	return data.Projects, nil
}
