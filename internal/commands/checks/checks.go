// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package checks

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	httpapi "github.com/cobaltcore-dev/cortex/internal/scheduler/api/http"
	cortexopenstack "github.com/cobaltcore-dev/cortex/internal/sync/openstack"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/hypervisors"
	"github.com/sapcc/go-bits/must"
)

// Run all checks.
func RunChecks(ctx context.Context, config conf.Config) {
	checkSchedulerReturnsValidHosts(ctx, config)
}

// Check that the scheduler returns a valid set of hosts.
func checkSchedulerReturnsValidHosts(ctx context.Context, config conf.Config) {
	osConf := config.GetSyncConfig().OpenStack
	slog.Info("authenticating against openstack", "url", osConf.Keystone.URL)
	authOptions := gophercloud.AuthOptions{
		IdentityEndpoint: osConf.Keystone.URL,
		Username:         osConf.Keystone.OSUsername,
		DomainName:       osConf.Keystone.OSUserDomainName,
		Password:         osConf.Keystone.OSPassword,
		AllowReauth:      true,
		Scope: &gophercloud.AuthScope{
			ProjectName: osConf.Keystone.OSProjectName,
			DomainName:  osConf.Keystone.OSProjectDomainName,
		},
	}
	pc := must.Return(openstack.NewClient(authOptions.IdentityEndpoint))
	must.Succeed(openstack.Authenticate(ctx, pc, authOptions))
	url := must.Return(pc.EndpointLocator(gophercloud.EndpointOpts{
		Type:         "compute",
		Availability: gophercloud.Availability(osConf.Nova.Availability),
	}))
	sc := &gophercloud.ServiceClient{ProviderClient: pc, Endpoint: url, Type: "compute"}
	slog.Info("authenticated against openstack", "url", url)
	slog.Info("listing hypervisors")
	pages := must.Return(hypervisors.List(sc, hypervisors.ListOpts{}).AllPages(ctx))
	var data = &struct {
		Hypervisors []cortexopenstack.Hypervisor `json:"hypervisors"`
	}{}
	must.Succeed(pages.(hypervisors.HypervisorPage).ExtractInto(data))
	if len(data.Hypervisors) == 0 {
		panic("no hypervisors found")
	}
	slog.Info("found hypervisors", "count", len(data.Hypervisors))

	var hosts []httpapi.ExternalSchedulerHost
	weights := make(map[string]float64)
	for _, h := range data.Hypervisors {
		weights[h.ServiceHost] = 1.0
		hosts = append(hosts, httpapi.ExternalSchedulerHost{
			ComputeHost:        h.ServiceHost,
			HypervisorHostname: h.Hostname,
		})
	}
	request := httpapi.ExternalSchedulerRequest{
		Hosts:   hosts,
		Weights: weights,
	}
	port := strconv.Itoa(config.GetAPIConfig().Port)
	apiURL := "http://cortex-scheduler:" + port + "/scheduler/nova/external"
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
	var resp httpapi.ExternalSchedulerResponse
	must.Succeed(json.NewDecoder(respRaw.Body).Decode(&resp))
	if len(resp.Hosts) == 0 {
		panic("no hosts found in response")
	}
	slog.Info("check successful, got hosts", "count", len(resp.Hosts))
}
