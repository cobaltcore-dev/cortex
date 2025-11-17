// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"context"
	"log/slog"
	"strings"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/domains"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
	"github.com/prometheus/client_golang/prometheus"
)

type IdentityAPI interface {
	Init(ctx context.Context) error
	// Retrieves all domains from the OpenStack identity service.
	GetAllDomains(ctx context.Context) ([]Domain, error)
	// Retrieves all projects from the OpenStack identity service.
	GetAllProjects(ctx context.Context) ([]Project, error)
}

type identityAPI struct {
	mon         datasources.Monitor
	keystoneAPI keystone.KeystoneAPI
	sc          *gophercloud.ServiceClient
	conf        v1alpha1.IdentityDatasource
}

func NewIdentityAPI(mon datasources.Monitor, k keystone.KeystoneAPI, conf v1alpha1.IdentityDatasource) IdentityAPI {
	return &identityAPI{mon: mon, keystoneAPI: k, conf: conf}
}

func (api *identityAPI) Init(ctx context.Context) error {
	if err := api.keystoneAPI.Authenticate(ctx); err != nil {
		return err
	}
	provider := api.keystoneAPI.Client()
	serviceType := "identity"
	url, err := api.keystoneAPI.FindEndpoint("public", serviceType)
	if err != nil {
		return err
	}
	slog.Info("using identity endpoint", "url", url)
	api.sc = &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       url,
		Type:           serviceType,
	}
	return nil
}

// Get all the domains from the OpenStack identity service.
func (api *identityAPI) GetAllDomains(ctx context.Context) ([]Domain, error) {
	label := Domain{}.TableName()
	slog.Info("fetching identity data", "label", "domains")
	if api.mon.RequestTimer != nil {
		hist := api.mon.RequestTimer.WithLabelValues(label)
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}
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
	label := Project{}.TableName()
	slog.Info("fetching identity data", "label", "projects")
	if api.mon.RequestTimer != nil {
		hist := api.mon.RequestTimer.WithLabelValues(label)
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}
	client := api.sc
	allPages, err := projects.List(client, nil).AllPages(ctx)
	if err != nil {
		return nil, err
	}

	var data = &struct {
		Projects []RawProject `json:"projects"`
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
