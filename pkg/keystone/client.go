// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package keystone

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Kubernetes connector which initializes the openstack connection from a secret.
type Connector struct {
	// Kubernetes API client to use.
	client.Client
	// Optional HTTP client to use for requests.
	HTTPClient *http.Client
}

// Create a new keystone client with authentication from the provided secret reference.
func (c Connector) FromSecretRef(ctx context.Context, ref corev1.SecretReference) (KeystoneClient, error) {
	authSecret := &corev1.Secret{}
	if err := c.Get(ctx, client.ObjectKey{
		Namespace: ref.Namespace,
		Name:      ref.Name,
	}, authSecret); err != nil {
		return nil, err
	}
	url, ok := authSecret.Data["url"]
	if !ok {
		return nil, errors.New("missing url in auth secret")
	}
	availability, ok := authSecret.Data["availability"]
	if !ok {
		return nil, errors.New("missing availability in auth secret")
	}
	osUsername, ok := authSecret.Data["username"]
	if !ok {
		return nil, errors.New("missing username in auth secret")
	}
	osPassword, ok := authSecret.Data["password"]
	if !ok {
		return nil, errors.New("missing password in auth secret")
	}
	osProjectName, ok := authSecret.Data["projectName"]
	if !ok {
		return nil, errors.New("missing projectName in auth secret")
	}
	osUserDomainName, ok := authSecret.Data["userDomainName"]
	if !ok {
		return nil, errors.New("missing userDomainName in auth secret")
	}
	osProjectDomainName, ok := authSecret.Data["projectDomainName"]
	if !ok {
		return nil, errors.New("missing projectDomainName in auth secret")
	}
	keystoneConf := conf.KeystoneConfig{
		URL:                 string(url),
		Availability:        string(availability),
		OSUsername:          string(osUsername),
		OSPassword:          string(osPassword),
		OSProjectName:       string(osProjectName),
		OSUserDomainName:    string(osUserDomainName),
		OSProjectDomainName: string(osProjectDomainName),
	}
	var keystoneClient KeystoneClient
	if c.HTTPClient != nil {
		keystoneClient = NewKeystoneClientWithHTTPClient(keystoneConf, c.HTTPClient)
	} else {
		keystoneClient = NewKeystoneClient(keystoneConf)
	}
	if err := keystoneClient.Authenticate(ctx); err != nil {
		return nil, err
	}
	return keystoneClient, nil
}

// KeystoneClient for OpenStack.
type KeystoneClient interface {
	// Authenticate against the OpenStack keystone.
	Authenticate(context.Context) error
	// Get the OpenStack provider client.
	Client() *gophercloud.ProviderClient
	// Find the endpoint for the given service type and availability.
	FindEndpoint(availability, serviceType string) (string, error)
	// Get the configured availability for keystone.
	Availability() string
}

// KeystoneClient implementation.
type keystoneClient struct {
	// OpenStack provider client.
	client *gophercloud.ProviderClient
	// OpenStack keystone configuration.
	keystoneConf conf.KeystoneConfig
	// Optional HTTP client to use for requests.
	httpClient *http.Client
}

// Create a new OpenStack keystone API client.
func NewKeystoneClient(keystoneConf conf.KeystoneConfig) KeystoneClient {
	return &keystoneClient{keystoneConf: keystoneConf}
}

// Create a new OpenStack keystone API with a custom HTTP client.
func NewKeystoneClientWithHTTPClient(keystoneConf conf.KeystoneConfig, httpClient *http.Client) KeystoneClient {
	return &keystoneClient{keystoneConf: keystoneConf, httpClient: httpClient}
}

// Authenticate against OpenStack keystone.
func (api *keystoneClient) Authenticate(ctx context.Context) error {
	if api.client != nil {
		// Already authenticated.
		return nil
	}
	slog.Info("authenticating against openstack", "url", api.keystoneConf.URL)
	authOptions := gophercloud.AuthOptions{
		IdentityEndpoint: api.keystoneConf.URL,
		Username:         api.keystoneConf.OSUsername,
		DomainName:       api.keystoneConf.OSUserDomainName,
		Password:         api.keystoneConf.OSPassword,
		AllowReauth:      true,
		Scope: &gophercloud.AuthScope{
			ProjectName: api.keystoneConf.OSProjectName,
			DomainName:  api.keystoneConf.OSProjectDomainName,
		},
	}
	provider, err := openstack.NewClient(authOptions.IdentityEndpoint)
	if err != nil {
		return err
	}
	if api.httpClient != nil {
		provider.HTTPClient = *api.httpClient
	}
	if err = openstack.Authenticate(ctx, provider, authOptions); err != nil {
		return err
	}
	api.client = provider
	slog.Info("authenticated against openstack")
	return nil
}

// Find the endpoint for the given service type and availability.
func (api *keystoneClient) FindEndpoint(availability, serviceType string) (string, error) {
	return api.client.EndpointLocator(gophercloud.EndpointOpts{
		Type:         serviceType,
		Availability: gophercloud.Availability(availability),
	})
}

func (api *keystoneClient) Availability() string {
	return api.keystoneConf.Availability
}

// Get the OpenStack provider client.
func (api *keystoneClient) Client() *gophercloud.ProviderClient {
	return api.client
}
