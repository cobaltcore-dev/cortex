// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"context"
	"log/slog"
	"strings"

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
		Projects []struct {
			ID       string   `json:"id"`
			Name     string   `json:"name"`
			DomainID string   `json:"domain_id"`
			ParentID string   `json:"parent_id"`
			IsDomain bool     `json:"is_domain"`
			Enabled  bool     `json:"enabled"`
			Tags     []string `json:"tags"`
		} `json:"projects"`
	}{}
	if err := allPages.(projects.ProjectPage).ExtractInto(data); err != nil {
		return nil, err
	}
	var result []Project
	for _, p := range data.Projects {
		result = append(result, Project{
			ID:       p.ID,
			Name:     p.Name,
			DomainID: p.DomainID,
			ParentID: p.ParentID,
			IsDomain: p.IsDomain,
			Enabled:  p.Enabled,
			Tags:     strings.Join(p.Tags, ","),
		})
	}
	slog.Info("fetched identity data", "label", "projects", "count", len(data.Projects))
	return result, nil
}
