// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/manila/api"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/sharedfilesystems/v2/schedulerstats"
	"github.com/sapcc/go-bits/must"
)

// Run all checks.
func RunChecks(ctx context.Context, config conf.Config) {
	checkManilaSchedulerReturnsValidHosts(ctx, config)
}

// Check that the Manila external scheduler returns a valid set of share hosts.
func checkManilaSchedulerReturnsValidHosts(ctx context.Context, config conf.Config) {
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
	// Workaround to find the v2 service of manila.
	// See: https://github.com/gophercloud/gophercloud/issues/3347
	gophercloud.ServiceTypeAliases["shared-file-system"] = []string{"sharev2"}
	sc := must.Return(openstack.NewSharedFileSystemV2(pc, gophercloud.EndpointOpts{
		Type:         "sharev2",
		Availability: gophercloud.Availability(osConf.Manila.Availability),
	}))
	sc.Microversion = "2.65"
	slog.Info("authenticated against openstack", "url", sc.Endpoint)
	slog.Info("listing share hosts")
	pages := must.Return(schedulerstats.List(sc, schedulerstats.ListOpts{}).AllPages(ctx))
	pools := must.Return(schedulerstats.ExtractPools(pages))
	if len(pools) == 0 {
		panic("no storage pools found")
	}
	slog.Info("found storage pools", "count", len(pools))

	var hosts []api.ExternalSchedulerHost
	weights := make(map[string]float64)
	for _, pool := range pools {
		// pool.Name is something like opencloud@alpha#ALPHA_pool
		// Which is: <host>@<backend>#<pool> in Manila slang.
		weights[pool.Name] = 1.0
		hosts = append(hosts, api.ExternalSchedulerHost{
			ShareHost: pool.Name,
		})
	}
	request := api.ExternalSchedulerRequest{
		Hosts:   hosts,
		Weights: weights,
	}
	port := strconv.Itoa(config.GetAPIConfig().Port)
	apiURL := "http://cortex-manila-scheduler:" + port + "/scheduler/manila/external"
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
		panic("no share hosts found in response")
	}
	slog.Info("check successful, got share hosts", "count", len(resp.Hosts))
}
