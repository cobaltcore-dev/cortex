// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strconv"
	"strings"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/identity"
	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/nova"
	api "github.com/cobaltcore-dev/cortex/scheduler/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/conf"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/aggregates"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/domains"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
	"github.com/sapcc/go-bits/must"
)

const (
	// The number of requests to send.
	nRandomRequestsToSend = 50
)

// Data necessary to generate a somewhat valid nova scheduler request.
type datacenter struct {
	hypervisors []nova.Hypervisor
	flavors     []nova.Flavor
	aggregates  []nova.RawAggregate
	projects    []identity.RawProject
	domains     []identity.Domain
	azs         []string
}

// Get hypervisors from openstack nova.
// Note: currently we need to fetch this without gophercloud.
// Gophercloud will just assume the request is a single page even when
// the response is paginated, returning only the first page.
func getHypervisors(ctx context.Context, sc *gophercloud.ServiceClient) ([]nova.Hypervisor, error) {
	initialURL := sc.Endpoint + "os-hypervisors/detail"
	var nextURL = &initialURL
	var hypervisors []nova.Hypervisor
	for nextURL != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, *nextURL, http.NoBody)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Auth-Token", sc.Token())
		req.Header.Set("X-OpenStack-Nova-API-Version", sc.Microversion)
		resp, err := sc.HTTPClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}
		var list struct {
			Hypervisors []nova.Hypervisor `json:"hypervisors"`
			Links       []struct {
				Rel  string `json:"rel"`
				Href string `json:"href"`
			} `json:"hypervisors_links"`
		}
		err = json.NewDecoder(resp.Body).Decode(&list)
		if err != nil {
			return nil, err
		}
		hypervisors = append(hypervisors, list.Hypervisors...)
		nextURL = nil
		for _, link := range list.Links {
			if link.Rel == "next" {
				nextURL = &link.Href
				break
			}
		}
	}
	return hypervisors, nil
}

// Prepare the test by fetching the necessary data from OpenStack.
func prepare(ctx context.Context, config conf.Config) datacenter {
	keystoneConf := config.KeystoneConfig
	osConf := config.SyncConfig.OpenStack
	slog.Info("authenticating against openstack", "url", keystoneConf.URL)
	authOptions := gophercloud.AuthOptions{
		IdentityEndpoint: keystoneConf.URL,
		Username:         keystoneConf.OSUsername,
		DomainName:       keystoneConf.OSUserDomainName,
		Password:         keystoneConf.OSPassword,
		AllowReauth:      true,
		Scope: &gophercloud.AuthScope{
			ProjectName: keystoneConf.OSProjectName,
			DomainName:  keystoneConf.OSProjectDomainName,
		},
	}
	pc := must.Return(openstack.NewClient(authOptions.IdentityEndpoint))
	must.Succeed(openstack.Authenticate(ctx, pc, authOptions))
	slog.Info("authenticated against openstack", "keystone", keystoneConf.URL)

	slog.Info("locating nova endpoint")
	novaURL := must.Return(pc.EndpointLocator(gophercloud.EndpointOpts{
		Type:         "compute",
		Availability: gophercloud.Availability(osConf.Nova.Availability),
	}))
	novaSC := &gophercloud.ServiceClient{
		ProviderClient: pc,
		Endpoint:       novaURL,
		Type:           "compute",
		// Since 2.53, the hypervisor id and service id is a UUID.
		// Since 2.61, the extra_specs are returned in the flavor details.
		Microversion: "2.61",
	}
	slog.Info("nova endpoint found", "novaURL", novaURL)

	slog.Info("listing hypervisors")
	hypervisors := must.Return(getHypervisors(ctx, novaSC))
	if len(hypervisors) == 0 {
		panic("no hypervisors found")
	}
	slog.Info("found hypervisors", "count", len(hypervisors))

	slog.Info("listing flavors")
	flo := flavors.ListOpts{AccessType: flavors.AllAccess}
	pages := must.Return(flavors.ListDetail(novaSC, flo).AllPages(ctx))
	dataFlavors := &struct {
		Flavors []nova.Flavor `json:"flavors"`
	}{}
	must.Succeed(pages.(flavors.FlavorPage).ExtractInto(dataFlavors))
	var flavors []nova.Flavor
	// Filter out bm flavors.
	for _, f := range dataFlavors.Flavors {
		// Cortex doesn't support baremetal flavors.
		// See: https://github.com/sapcc/nova/blob/5fcb125/nova/utils.py#L1234
		// And: https://github.com/sapcc/nova/pull/570/files
		if strings.Contains(f.ExtraSpecs, "capabilities:cpu_arch") {
			continue
		}
		flavors = append(flavors, f)
	}
	if len(flavors) == 0 {
		panic("no flavors found")
	}
	slog.Info("found non-bm flavors", "count", len(flavors))

	slog.Info("listing aggregates")
	pages = must.Return(aggregates.List(novaSC).AllPages(ctx))
	dataAggregates := &struct {
		Aggregates []nova.RawAggregate `json:"aggregates"`
	}{}
	must.Succeed(pages.(aggregates.AggregatesPage).ExtractInto(dataAggregates))
	aggregates := dataAggregates.Aggregates
	if len(aggregates) == 0 {
		panic("no aggregates found")
	}
	slog.Info("found aggregates", "count", len(aggregates))

	slog.Info("locating keystone endpoint")
	keystoneURL := must.Return(pc.EndpointLocator(gophercloud.EndpointOpts{
		Type:         "identity",
		Availability: gophercloud.Availability(osConf.Identity.Availability),
	}))
	keystoneSC := &gophercloud.ServiceClient{
		ProviderClient: pc,
		Endpoint:       keystoneURL,
		Type:           "identity",
	}
	slog.Info("keystone endpoint found", "keystoneURL", keystoneURL)

	slog.Info("listing projects")
	pages = must.Return(projects.List(keystoneSC, projects.ListOpts{}).AllPages(ctx))
	dataProjects := &struct {
		Projects []identity.RawProject `json:"projects"`
	}{}
	must.Succeed(pages.(projects.ProjectPage).ExtractInto(dataProjects))
	projects := dataProjects.Projects
	if len(projects) == 0 {
		panic("no projects found")
	}
	slog.Info("found projects", "count", len(projects))

	slog.Info("listing domains")
	pages = must.Return(domains.List(keystoneSC, nil).AllPages(ctx))
	dataDomains := &struct {
		Domains []identity.Domain `json:"domains"`
	}{}
	must.Succeed(pages.(domains.DomainPage).ExtractInto(dataDomains))
	domains := dataDomains.Domains
	if len(domains) == 0 {
		panic("no domains found")
	}
	slog.Info("found domains", "count", len(domains))

	azs := make(map[string]struct{})
	for _, a := range aggregates {
		if a.AvailabilityZone == nil {
			continue // Skip aggregates without an availability zone.
		}
		azs[*a.AvailabilityZone] = struct{}{}
	}
	azsSlice := make([]string, 0, len(azs))
	for az := range azs {
		azsSlice = append(azsSlice, az)
	}
	if len(azsSlice) == 0 {
		panic("no availability zones found")
	}
	slog.Info("found availability zones", "count", len(azsSlice))

	return datacenter{
		hypervisors: hypervisors,
		flavors:     flavors,
		aggregates:  aggregates,
		projects:    projects,
		domains:     domains,
		azs:         azsSlice,
	}
}

// Generate external scheduler requests with the given datacenter data.
func randomRequest(dc datacenter, seed int) api.ExternalSchedulerRequest {
	// Create a new random source with the given seed.
	//nolint:gosec // We don't care if the random source is cryptographically secure.
	randSource := rand.New(rand.NewSource(int64(seed)))
	// Select all hosts for now.
	var hosts []api.ExternalSchedulerHost
	weights := make(map[string]float64)
	for _, h := range dc.hypervisors {
		weights[h.ServiceHost] = 0.0
		hosts = append(hosts, api.ExternalSchedulerHost{
			ComputeHost:        h.ServiceHost,
			HypervisorHostname: h.Hostname,
		})
	}
	// Get a random az.
	az := dc.azs[randSource.Intn(len(dc.azs))]
	slog.Info("using availability zone", "az", az)
	project := dc.projects[randSource.Intn(len(dc.projects))]
	slog.Info("using project", "projectID", project.ID, "projectName", project.Name)
	// Get the domain for the project.
	domainsByID := make(map[string]identity.Domain)
	for _, d := range dc.domains {
		domainsByID[d.ID] = d
	}
	domain, ok := domainsByID[project.DomainID]
	if !ok {
		panic("project domain not found")
	}
	slog.Info("using domain", "domainID", domain.ID, "domainName", domain.Name)
	// Get a random flavor.
	flavor := dc.flavors[randSource.Intn(len(dc.flavors))]
	slog.Info("using flavor", "flavorName", flavor.Name, "flavorID", flavor.ID)
	// JSON unmarshal the extra specs to a string.
	var extraSpecs map[string]string
	if flavor.ExtraSpecs == "" {
		extraSpecs = make(map[string]string)
	} else if err := json.Unmarshal([]byte(flavor.ExtraSpecs), &extraSpecs); err != nil {
		panic(err)
	}
	slog.Info("using flavor extra specs", "extraSpecs", extraSpecs)
	request := api.ExternalSchedulerRequest{
		Spec: api.NovaObject[api.NovaSpec]{Data: api.NovaSpec{
			InstanceUUID:     "cortex-e2e-tests",
			AvailabilityZone: az,
			ProjectID:        project.ID,
			Flavor: api.NovaObject[api.NovaFlavor]{Data: api.NovaFlavor{
				Name:       flavor.Name,
				MemoryMB:   flavor.RAM,
				VCPUs:      flavor.VCPUs,
				ExtraSpecs: extraSpecs,
			}},
			SchedulerHints: map[string]any{
				"domain_name": []string{domain.Name},
			},
		}},
		Hosts:   hosts,
		Weights: weights,
	}
	return request
}

// Check that the nova external scheduler returns a valid set of hosts.
func checkNovaSchedulerReturnsValidHosts(
	ctx context.Context,
	config conf.Config,
	req api.ExternalSchedulerRequest,
) []string {

	port := strconv.Itoa(config.APIConfig.Port)
	apiURL := "http://cortex-nova-scheduler:" + port + "/scheduler/nova/external"
	slog.Info("sending request to external scheduler", "apiURL", apiURL)

	requestBody := must.Return(json.Marshal(req))
	buf := bytes.NewBuffer(requestBody)
	httpReq := must.Return(http.NewRequestWithContext(ctx, http.MethodPost, apiURL, buf))
	httpReq.Header.Set("Content-Type", "application/json")
	//nolint:bodyclose // We don't care about the body here.
	respRaw := must.Return(http.DefaultClient.Do(httpReq))
	defer respRaw.Body.Close()
	if respRaw.StatusCode != http.StatusOK {
		// Log the response body for debugging
		bodyBytes := must.Return(io.ReadAll(respRaw.Body))
		slog.Error("external scheduler API returned non-200 status code",
			"statusCode", respRaw.StatusCode,
			"responseBody", string(bodyBytes),
		)
		panic("external scheduler API returned non-200 status code")
	}
	var resp api.ExternalSchedulerResponse
	must.Succeed(json.NewDecoder(respRaw.Body).Decode(&resp))
	return resp.Hosts
}

// Run all checks.
func RunChecks(ctx context.Context, config conf.Config) {
	datacenter := prepare(ctx, config)
	requestsWithHostsReturned := 0
	requestsWithNoHostsReturned := 0
	for i := range nRandomRequestsToSend {
		request := randomRequest(datacenter, i)
		hosts := checkNovaSchedulerReturnsValidHosts(ctx, config, request)
		if len(hosts) > 0 {
			requestsWithHostsReturned++
		} else {
			requestsWithNoHostsReturned++
		}
	}
	// Print a summary.
	slog.Info(
		"summary",
		"requestsWithHostsReturned", requestsWithHostsReturned,
		"requestsWithNoHostsReturned", requestsWithNoHostsReturned,
	)
}
