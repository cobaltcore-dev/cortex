// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package hypervisors

import (
	"context"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	v1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/sapcc/go-bits/must"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Client that can scrape the given hypervisor API servers.
type HypervisorsClient interface {
	// Create clients for all api servers in the config if not already done.
	Init()
	// List all hypervisors in the infrastructure.
	ListHypervisors(ctx context.Context) ([]v1.Hypervisor, error)
}

// Default implementation of the HypervisorsClient interface.
type hypervisorsClient struct {
	// Config containing credentials to contact the api servers.
	Config Conf
	// Authenticated kubernetes clients for each api server in the config.
	clients []client.Client
}

// Create a new client for scraping hypervisors with configuration loaded
// from /etc/config/conf.json overridden by /etc/secrets/secrets.json
func NewHypervisorsClient() HypervisorsClient {
	config := conf.NewConfig[Conf]()
	return &hypervisorsClient{Config: config}
}

// Initialize the clients if there are none yet.
func (c *hypervisorsClient) Init() {
	if c.clients != nil {
		return
	}
	for _, apiServer := range c.Config.HypervisorAPIServers {
		scheme := must.Return(v1.SchemeBuilder.Build())
		clientConfig := &rest.Config{
			Host:        apiServer.Host,
			APIPath:     apiServer.APIPath,
			BearerToken: apiServer.BearerToken,
			TLSClientConfig: rest.TLSClientConfig{
				CAData: []byte(apiServer.CACrt),
			},
		}
		cl, err := client.New(clientConfig, client.Options{Scheme: scheme})
		if err != nil {
			continue
		}
		c.clients = append(c.clients, cl)
	}
}

// List all hypervisors in the infrastructure.
func (c *hypervisorsClient) ListHypervisors(ctx context.Context) ([]v1.Hypervisor, error) {
	if c.clients == nil {
		c.Init()
	}
	var hypervisors []v1.Hypervisor
	for _, cl := range c.clients {
		var hvList v1.HypervisorList
		if err := cl.List(ctx, &hvList); err != nil {
			return nil, err
		}
		hypervisors = append(hypervisors, hvList.Items...)
	}
	return hypervisors, nil
}
