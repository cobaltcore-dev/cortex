// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	gosync "sync"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	v1 "github.com/cobaltcore-dev/cortex/internal/reservations/api/v1"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
	"github.com/sapcc/go-bits/must"
)

type OpenStackClient interface {
	Init(ctx context.Context)
	GetAllCommitments(ctx context.Context) ([]v1.Commitment, error)
}

type openStackClient struct {
	conf conf.KeystoneConfig

	provider *gophercloud.ProviderClient
	keystone *gophercloud.ServiceClient
	limes    *gophercloud.ServiceClient
}

func NewOpenStackClient(conf conf.KeystoneConfig) OpenStackClient {
	return &openStackClient{conf: conf}
}

func (o *openStackClient) Init(ctx context.Context) {
	slog.Info("authenticating against openstack", "url", o.conf.URL)
	authOptions := gophercloud.AuthOptions{
		IdentityEndpoint: o.conf.URL,
		Username:         o.conf.OSUsername,
		DomainName:       o.conf.OSUserDomainName,
		Password:         o.conf.OSPassword,
		AllowReauth:      true,
		Scope: &gophercloud.AuthScope{
			ProjectName: o.conf.OSProjectName,
			DomainName:  o.conf.OSProjectDomainName,
		},
	}
	httpClient := must.Return(sync.NewHTTPClient(o.conf.SSO))
	provider := must.Return(openstack.NewClient(authOptions.IdentityEndpoint))
	provider.HTTPClient = *httpClient
	must.Succeed(openstack.Authenticate(ctx, provider, authOptions))
	o.provider = provider
	slog.Info("authenticated against openstack")

	// Get the keystone endpoint.
	url := must.Return(o.provider.EndpointLocator(gophercloud.EndpointOpts{
		Type:         "identity",
		Availability: "public",
	}))
	slog.Info("using identity endpoint", "url", url)
	o.keystone = &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       url,
		Type:           "identity",
	}

	// Get the limes endpoint.
	url = must.Return(o.provider.EndpointLocator(gophercloud.EndpointOpts{
		Type:         "resources",
		Availability: "public",
	}))
	slog.Info("using limes endpoint", "url", url)
	o.limes = &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       url,
		Type:           "resources",
	}
}

// Get all available commitments from limes + keystone.
// This function fetches the commitments for each project in parallel.
func (o *openStackClient) GetAllCommitments(ctx context.Context) ([]v1.Commitment, error) {
	slog.Info("fetching projects from keystone")
	allPages, err := projects.List(o.keystone, nil).AllPages(ctx)
	if err != nil {
		return nil, err
	}
	var data = &struct {
		Projects []projects.Project `json:"projects"`
	}{}
	if err := allPages.(projects.ProjectPage).ExtractInto(data); err != nil {
		return nil, err
	}
	projects := data.Projects

	resultMutex := gosync.Mutex{}
	results := []v1.Commitment{}
	var wg gosync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Channel to communicate errors from goroutines.
	errChan := make(chan error, len(projects))
	for _, project := range projects {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Fetch commitments for the project.
			newResults, err := o.getCommitments(ctx, project)
			if err != nil {
				errChan <- err
				cancel()
				return
			}
			resultMutex.Lock()
			results = append(results, newResults...)
			resultMutex.Unlock()
		}()
		time.Sleep(50 * time.Millisecond) // Don't overload the API.
		if len(results) > 0 {
			break // Just for debugging.
		}
	}

	// Wait for all goroutines to finish and close the error channel.
	go func() {
		wg.Wait()
		close(errChan)
	}()
	// Return the first error encountered, if any.
	for err := range errChan {
		if err != nil {
			slog.Error("failed to resolve commitments", "error", err)
			return nil, err
		}
	}
	return results, nil
}

// Resolve the commitments for the given project.
func (o *openStackClient) getCommitments(ctx context.Context, project projects.Project) ([]v1.Commitment, error) {
	url := o.limes.Endpoint + "v1" +
		"/domains/" + project.DomainID +
		"/projects/" + project.ID +
		"/commitments"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", o.limes.Token())
	resp, err := o.limes.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	var list struct {
		Commitments []v1.Commitment `json:"commitments"`
	}
	err = json.NewDecoder(resp.Body).Decode(&list)
	if err != nil {
		return nil, err
	}
	// Add the project information to each commitment.
	for i := range list.Commitments {
		list.Commitments[i].ProjectID = project.ID
		list.Commitments[i].DomainID = project.DomainID
	}
	return list.Commitments, nil
}
