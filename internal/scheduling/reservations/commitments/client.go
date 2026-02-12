// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	gosync "sync"
	"time"

	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/cobaltcore-dev/cortex/pkg/sso"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
	"github.com/sapcc/go-bits/jobloop"
	"github.com/sapcc/go-bits/must"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Client to fetch commitments.
type CommitmentsClient interface {
	// Init the client.
	Init(ctx context.Context, client client.Client, conf SyncerConfig) error
	// List all projects to resolve commitments.
	ListProjects(ctx context.Context) ([]Project, error)
	// List all flavors by their name to resolve instance commitments.
	ListFlavorsByName(ctx context.Context) (map[string]Flavor, error)
	// List all commitments with resolved metadata (e.g. project, flavor, ...).
	ListCommitmentsByID(ctx context.Context, projects ...Project) (map[string]Commitment, error)
	// List all servers for the given projects from nova.
	// The result is a map from project ID to the list of servers.
	ListServersByProjectID(ctx context.Context, projects ...Project) (map[string][]Server, error)
}

// Commitments client fetching commitments from openstack services.
type commitmentsClient struct {
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
func NewCommitmentsClient() CommitmentsClient {
	return &commitmentsClient{}
}

// Init the client.
func (c *commitmentsClient) Init(ctx context.Context, client client.Client, conf SyncerConfig) error {
	var authenticatedHTTP = http.DefaultClient
	if conf.SSOSecretRef != nil {
		var err error
		authenticatedHTTP, err = sso.Connector{Client: client}.
			FromSecretRef(ctx, *conf.SSOSecretRef)
		if err != nil {
			return err
		}
	}
	authenticatedKeystone, err := keystone.
		Connector{Client: client, HTTPClient: authenticatedHTTP}.
		FromSecretRef(ctx, conf.KeystoneSecretRef)
	if err != nil {
		return err
	}
	c.provider = authenticatedKeystone.Client()

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
	return nil
}

// Get all Nova flavors by their name to resolve instance commitments.
func (c *commitmentsClient) ListFlavorsByName(ctx context.Context) (map[string]Flavor, error) {
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
	flavorsByName := make(map[string]Flavor, len(data.Flavors))
	for _, flavor := range data.Flavors {
		flavorsByName[flavor.Name] = flavor
	}
	return flavorsByName, nil
}

// Get all projects from Keystone to resolve commitments.
func (c *commitmentsClient) ListProjects(ctx context.Context) ([]Project, error) {
	syncLog.Info("fetching projects from keystone")
	allPages, err := projects.List(c.keystone, nil).AllPages(ctx)
	if err != nil {
		return nil, err
	}
	var data = &struct {
		Projects []Project `json:"projects"`
	}{}
	if err := allPages.(projects.ProjectPage).ExtractInto(data); err != nil {
		return nil, err
	}
	syncLog.Info("fetched projects from keystone", "count", len(data.Projects))
	return data.Projects, nil
}

// Get all available commitments from limes + keystone + nova.
// This function fetches the commitments for each project in parallel.
func (c *commitmentsClient) ListCommitmentsByID(ctx context.Context, projects ...Project) (map[string]Commitment, error) {
	syncLog.Info("fetching commitments from limes", "projects", len(projects))
	commitmentsMutex := gosync.Mutex{}
	commitments := make(map[string]Commitment)
	var wg gosync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Channel to communicate errors from goroutines.
	errChan := make(chan error, len(projects))
	for _, project := range projects {
		wg.Go(func() {
			// Fetch instance commitments for the project.
			newResults, err := c.listCommitments(ctx, project)
			if err != nil {
				errChan <- err
				cancel()
				return
			}
			commitmentsMutex.Lock()
			for _, c := range newResults {
				commitments[c.UUID] = c
			}
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
	return commitments, nil
}

// Resolve the commitments for the given project.
func (c *commitmentsClient) listCommitments(ctx context.Context, project Project) ([]Commitment, error) {
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
		c.ProjectID = project.ID
		c.DomainID = project.DomainID
		commitments = append(commitments, c)
	}
	return commitments, nil
}

// Get all servers for the given project ids from nova.
// The result is a map from project ID to the list of servers.
func (c *commitmentsClient) ListServersByProjectID(ctx context.Context, projects ...Project) (map[string][]Server, error) {
	syncLog.Info("fetching servers from nova")
	serversByProject := make(map[string][]Server, len(projects))
	var mu gosync.Mutex
	var wg gosync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Channel to communicate errors from goroutines.
	errChan := make(chan error, len(projects))
	for _, project := range projects {
		wg.Go(func() {
			servers, err := c.listServersForProject(ctx, project)
			if err != nil {
				errChan <- err
				cancel()
				return
			}
			mu.Lock()
			serversByProject[project.ID] = servers
			mu.Unlock()
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
			syncLog.Error(err, "failed to fetch servers")
			return nil, err
		}
	}
	syncLog.Info("fetched servers from nova", "projects", len(serversByProject))
	return serversByProject, nil
}

// Get all servers for the given project id from nova.
func (c *commitmentsClient) listServersForProject(ctx context.Context, project Project) ([]Server, error) {
	lo := servers.ListOpts{
		// AllTenants must be set to fetch servers from other projects
		// than the one we are authenticated with.
		AllTenants: true,
		TenantID:   project.ID,
	}
	pages, err := servers.List(c.nova, lo).AllPages(ctx)
	if err != nil {
		return nil, err
	}
	// Parse the json data into our custom model.
	var data = &struct {
		Servers []Server `json:"servers"`
	}{}
	if err := pages.(servers.ServerPage).ExtractInto(data); err != nil {
		return nil, err
	}
	syncLog.Info("fetched servers for project", "project", project.ID, "count", len(data.Servers))
	return data.Servers, nil
}
