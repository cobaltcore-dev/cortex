// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cobaltcore-dev/cortex/lib/keystone"
	"github.com/cobaltcore-dev/cortex/lib/sso"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/sapcc/go-bits/must"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	Init(ctx context.Context, client client.Client, conf Config) error
	// List all hypervisors.
	ListHypervisors(ctx context.Context) ([]Hypervisor, error)
}

// Hypervisor client fetching commitments from openstack services.
type hypervisorClient struct {
	// Providerclient authenticated against openstack.
	provider *gophercloud.ProviderClient
	// Nova service client for OpenStack.
	nova *gophercloud.ServiceClient
}

// Create a new hypervisor client.
// By default, this client will fetch hypervisors from the nova API.
func NewHypervisorClient() HypervisorClient {
	return &hypervisorClient{}
}

// Init the client.
func (c *hypervisorClient) Init(ctx context.Context, client client.Client, conf Config) error {
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
	// Automatically fetch the nova endpoint from the keystone service catalog.
	c.provider = authenticatedKeystone.Client()

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
	return nil
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
