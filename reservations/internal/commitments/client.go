// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	gosync "sync"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/keystone"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
	"github.com/sapcc/go-bits/jobloop"
	"github.com/sapcc/go-bits/must"
)

// Client to fetch commitments.
type CommitmentsClient interface {
	// Init the client.
	Init(ctx context.Context)
	// Get all commitments with resolved metadata (e.g. project, flavor, ...).
	GetComputeCommitments(ctx context.Context) ([]Commitment, error)
}

// Commitments client fetching commitments from openstack services.
type commitmentsClient struct {
	// Basic config to authenticate against openstack.
	conf conf.KeystoneConfig

	// Providerclient authenticated against openstack.
	provider *gophercloud.ProviderClient
	// Keystone service client for OpenStack.
	keystone *gophercloud.ServiceClient
	// Nova service client for OpenStack.
	nova *gophercloud.ServiceClient
	// Limes service client for OpenStack.
	limes *gophercloud.ServiceClient
}

// Create a new commitments client.
// By default, this client will fetch commitments from the limes API.
func NewCommitmentsClient(conf conf.KeystoneConfig) CommitmentsClient {
	return &commitmentsClient{conf: conf}
}

// Init the client.
func (c *commitmentsClient) Init(ctx context.Context) {
	syncLog.Info("authenticating against openstack", "url", c.conf.URL)
	auth := keystone.NewKeystoneAPI(c.conf)
	must.Succeed(auth.Authenticate(ctx))
	c.provider = auth.Client()
	syncLog.Info("authenticated against openstack")

	// Get the keystone endpoint.
	url := must.Return(c.provider.EndpointLocator(gophercloud.EndpointOpts{
		Type:         "identity",
		Availability: "public",
	}))
	syncLog.Info("using identity endpoint", "url", url)
	c.keystone = &gophercloud.ServiceClient{
		ProviderClient: c.provider,
		Endpoint:       url,
		Type:           "identity",
	}

	// Get the nova endpoint.
	url = must.Return(c.provider.EndpointLocator(gophercloud.EndpointOpts{
		Type:         "compute",
		Availability: "public",
	}))
	syncLog.Info("using nova endpoint", "url", url)
	c.nova = &gophercloud.ServiceClient{
		ProviderClient: c.provider,
		Endpoint:       url,
		Type:           "compute",
		Microversion:   "2.61",
	}

	// Get the limes endpoint.
	url = must.Return(c.provider.EndpointLocator(gophercloud.EndpointOpts{
		Type:         "resources",
		Availability: "public",
	}))
	syncLog.Info("using limes endpoint", "url", url)
	c.limes = &gophercloud.ServiceClient{
		ProviderClient: c.provider,
		Endpoint:       url,
		Type:           "resources",
	}
}

// Get all Nova flavors by their name to resolve instance commitments.
func (c *commitmentsClient) GetAllFlavors(ctx context.Context) ([]Flavor, error) {
	syncLog.Info("fetching all flavors from nova")
	flo := flavors.ListOpts{AccessType: flavors.AllAccess}
	pages, err := flavors.ListDetail(c.nova, flo).AllPages(ctx)
	if err != nil {
		return nil, err
	}
	// Parse the json data into our custom model.
	var data = &struct {
		Flavors []Flavor `json:"flavors"`
	}{}
	if err := pages.(flavors.FlavorPage).ExtractInto(data); err != nil {
		return nil, err
	}
	syncLog.Info("fetched flavors from nova", "count", len(data.Flavors))
	return data.Flavors, nil
}

// Get all projects from Keystone to resolve commitments.
func (c *commitmentsClient) GetAllProjects(ctx context.Context) ([]projects.Project, error) {
	syncLog.Info("fetching projects from keystone")
	allPages, err := projects.List(c.keystone, nil).AllPages(ctx)
	if err != nil {
		return nil, err
	}
	var data = &struct {
		Projects []projects.Project `json:"projects"`
	}{}
	if err := allPages.(projects.ProjectPage).ExtractInto(data); err != nil {
		return nil, err
	}
	syncLog.Info("fetched projects from keystone", "count", len(data.Projects))
	return data.Projects, nil
}

// Get all available commitments from limes + keystone + nova.
// This function fetches the commitments for each project in parallel.
func (c *commitmentsClient) GetComputeCommitments(ctx context.Context) ([]Commitment, error) {
	projects, err := c.GetAllProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get projects: %w", err)
	}
	syncLog.Info("fetching flavor commitments from limes", "projects", len(projects))
	commitmentsMutex := gosync.Mutex{}
	commitments := []Commitment{}
	var wg gosync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Channel to communicate errors from goroutines.
	errChan := make(chan error, len(projects))
	for _, project := range projects {
		wg.Go(func() {
			// Fetch instance commitments for the project.
			newResults, err := c.getCommitments(ctx, project)
			if err != nil {
				errChan <- err
				cancel()
				return
			}
			commitmentsMutex.Lock()
			commitments = append(commitments, newResults...)
			commitmentsMutex.Unlock()
		})
		time.Sleep(jobloop.DefaultJitter(50 * time.Millisecond)) // Don't overload the API.
	}
	// Wait for all goroutines to finish and close the error channel.
	go func() {
		wg.Wait()
		close(errChan)
	}()
	// Return the first error encountered, if any.
	for err := range errChan {
		if err != nil {
			syncLog.Error(err, "failed to resolve commitments")
			return nil, err
		}
	}
	syncLog.Info("resolved commitments from limes", "count", len(commitments))

	flavors, err := c.GetAllFlavors(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get flavors: %w", err)
	}
	// Resolve the flavor for each commitment.
	flavorsByName := make(map[string]Flavor, len(flavors))
	for _, flavor := range flavors {
		flavorsByName[flavor.Name] = flavor
	}
	for i := range commitments {
		if !strings.HasPrefix(commitments[i].ResourceName, "instances_") {
			// Not an instance commitment.
			continue
		}
		flavorName := strings.TrimPrefix(commitments[i].ResourceName, "instances_")
		if flavor, ok := flavorsByName[flavorName]; ok {
			commitments[i].Flavor = &flavor
		} else {
			syncLog.Info("flavor not found for commitment", "flavor", flavorName, "commitment_id", commitments[i].ID)
		}
	}
	return commitments, nil
}

// Resolve the commitments for the given project.
func (c *commitmentsClient) getCommitments(ctx context.Context, project projects.Project) ([]Commitment, error) {
	url := c.limes.Endpoint + "v1" +
		"/domains/" + project.DomainID +
		"/projects/" + project.ID +
		"/commitments"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", c.limes.Token())
	resp, err := c.limes.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	var list struct {
		Commitments []Commitment `json:"commitments"`
	}
	err = json.NewDecoder(resp.Body).Decode(&list)
	if err != nil {
		return nil, err
	}
	// Add the project information to each commitment.
	var commitments []Commitment
	for _, c := range list.Commitments {
		if c.ServiceType != "compute" {
			// Not a compute commitment.
			continue
		}
		c.ProjectID = project.ID
		c.DomainID = project.DomainID
		commitments = append(commitments, c)
	}
	return commitments, nil
}
