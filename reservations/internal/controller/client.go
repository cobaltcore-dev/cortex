// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/keystone"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/sapcc/go-bits/must"
	ctrl "sigs.k8s.io/controller-runtime"
)

var (
	syncLog = ctrl.Log.WithName("sync")
)

// OpenStack hypervisor model as returned by the Nova API under /os-hypervisors/detail.
// See: https://docs.openstack.org/api-ref/compute/#list-hypervisors-details
type Hypervisor struct {
	ID       string `json:"id"`
	Hostname string `json:"hypervisor_hostname"`
	Service  struct {
		Host string `json:"host"`
	} `json:"service"`
	Type string `json:"hypervisor_type"`
}

// Client to fetch hypervisor data.
type HypervisorClient interface {
	// Init the client.
	Init(ctx context.Context)
	// List all hypervisors.
	ListHypervisors(ctx context.Context) ([]Hypervisor, error)
}

// Hypervisor client fetching commitments from openstack services.
type hypervisorClient struct {
	// Basic config to authenticate against openstack.
	conf conf.KeystoneConfig

	// Providerclient authenticated against openstack.
	provider *gophercloud.ProviderClient
	// Nova service client for OpenStack.
	nova *gophercloud.ServiceClient
}

// Create a new hypervisor client.
// By default, this client will fetch hypervisors from the nova API.
func NewHypervisorClient(conf conf.KeystoneConfig) HypervisorClient {
	return &hypervisorClient{conf: conf}
}

// Init the client.
func (c *hypervisorClient) Init(ctx context.Context) {
	syncLog.Info("authenticating against openstack", "url", c.conf.URL)
	auth := keystone.NewKeystoneAPI(c.conf)
	must.Succeed(auth.Authenticate(ctx))
	c.provider = auth.Client()
	syncLog.Info("authenticated against openstack")

	// Get the nova endpoint.
	url := must.Return(c.provider.EndpointLocator(gophercloud.EndpointOpts{
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
}

func (c *hypervisorClient) ListHypervisors(ctx context.Context) ([]Hypervisor, error) {
	// Note: currently we need to fetch this without gophercloud.
	// Gophercloud will just assume the request is a single page even when
	// the response is paginated, returning only the first page.
	initialURL := c.nova.Endpoint + "os-hypervisors/detail"
	var nextURL = &initialURL
	var hypervisors []Hypervisor
	for nextURL != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, *nextURL, http.NoBody)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Auth-Token", c.provider.Token())
		req.Header.Set("X-OpenStack-Nova-API-Version", c.nova.Microversion)
		resp, err := c.nova.HTTPClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}
		var list struct {
			Hypervisors []Hypervisor `json:"hypervisors"`
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
