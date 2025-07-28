// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/hypervisors"
	"github.com/sapcc/go-bits/must"
)

// Run all checks.
func RunChecks(ctx context.Context, config conf.Config) {
	checkNovaSchedulerReturnsValidHosts(ctx, config)
}

// Check that the nova external scheduler returns a valid set of hosts.
func checkNovaSchedulerReturnsValidHosts(ctx context.Context, config conf.Config) {
	keystoneConf := config.GetKeystoneConfig()
	osConf := config.GetSyncConfig().OpenStack
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
	url := must.Return(pc.EndpointLocator(gophercloud.EndpointOpts{
		Type:         "compute",
		Availability: gophercloud.Availability(osConf.Nova.Availability),
	}))
	sc := &gophercloud.ServiceClient{
		ProviderClient: pc,
		Endpoint:       url,
		Type:           "compute",
		// Since microversion 2.53, the hypervisor id and service id is a UUID.
		Microversion: "2.53",
	}
	slog.Info("authenticated against openstack", "url", url)
	slog.Info("listing hypervisors")
	pages := must.Return(hypervisors.List(sc, hypervisors.ListOpts{}).AllPages(ctx))
	var data = &struct {
		Hypervisors []nova.Hypervisor `json:"hypervisors"`
	}{}
	must.Succeed(pages.(hypervisors.HypervisorPage).ExtractInto(data))
	if len(data.Hypervisors) == 0 {
		panic("no hypervisors found")
	}
	slog.Info("found hypervisors", "count", len(data.Hypervisors))

	var hosts []api.ExternalSchedulerHost
	weights := make(map[string]float64)
	for _, h := range data.Hypervisors {
		weights[h.ServiceHost] = 1.0
		hosts = append(hosts, api.ExternalSchedulerHost{
			ComputeHost:        h.ServiceHost,
			HypervisorHostname: h.Hostname,
		})
	}
	request := api.ExternalSchedulerRequest{
		Hosts:   hosts,
		Weights: weights,
	}
	port := strconv.Itoa(config.GetAPIConfig().Port)
	apiURL := "http://cortex-nova-scheduler:" + port + "/scheduler/nova/external"
	slog.Info("sending request to external scheduler", "apiURL", apiURL)

	requestBody := must.Return(json.Marshal(request))
	buf := bytes.NewBuffer(requestBody)
	req := must.Return(http.NewRequestWithContext(ctx, http.MethodPost, apiURL, buf))
	req.Header.Set("Content-Type", "application/json")
	//nolint:bodyclose // We don't care about the body here.
	respRaw := must.Return(http.DefaultClient.Do(req))
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
	if len(resp.Hosts) == 0 {
		panic("no hosts found in response")
	}
	slog.Info("check successful, got compute hosts", "count", len(resp.Hosts))
}
