// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"context"
	"log/slog"
	"net/url"
	"strings"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/cobaltcore-dev/cortex/pkg/openstack"
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
	client      *openstack.OpenstackClient
	conf        v1alpha1.IdentityDatasource
}

func NewIdentityAPI(mon datasources.Monitor, k keystone.KeystoneAPI, conf v1alpha1.IdentityDatasource) IdentityAPI {
	return &identityAPI{mon: mon, keystoneAPI: k, conf: conf}
}

func (api *identityAPI) Init(ctx context.Context) error {
	client, err := openstack.IdentityClient(ctx, api.keystoneAPI)
	if err != nil {
		return err
	}
	api.client = client
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

	var domains []Domain
	if err := api.client.List(ctx, "domains", nil, "domains", &domains); err != nil {
		return nil, err
	}

	slog.Info("fetched identity data", "label", "domains", "count", len(domains))
	return domains, nil
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

	var rawProjects []RawProject
	if err := api.client.List(ctx, "projects", url.Values{}, "projects", &rawProjects); err != nil {
		return nil, err
	}

	slog.Info("fetched identity data", "label", "projects", "count", len(rawProjects))
	var projects []Project
	for _, p := range rawProjects {
		projects = append(projects, Project{
			ID:       p.ID,
			Name:     p.Name,
			DomainID: p.DomainID,
			ParentID: p.ParentID,
			IsDomain: p.IsDomain,
			Enabled:  p.Enabled,
			Tags:     strings.Join(p.Tags, ","),
		})
	}
	slog.Info("fetched identity data", "label", "projects", "count", len(projects))
	return projects, nil
}
