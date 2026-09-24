// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package inflight

import (
	"context"
	"errors"
	"net/http"

	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/cobaltcore-dev/cortex/pkg/sso"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type VMClient interface {
	StartWithKubernetesSecrets(ctx context.Context, client client.Client) error
	// GetCurrentVMSize returns the size of the VM with the given ID,
	// or an error if the VM cannot be found.
	GetCurrentVMSize(ctx context.Context, vmID string) (map[hv1.ResourceName]resource.Quantity, error)
}

type novaVMClient struct {
	// config is the configuration for the novaVMClient, including keystone and SSO credentials.
	config NovaVMClientConfig
	// sc is the service client for the OpenStack Nova API.
	sc *gophercloud.ServiceClient
}

type NovaVMClientConfig struct {
	// Secret ref to keystone credentials stored in a k8s secret.
	KeystoneSecretRef corev1.SecretReference `json:"keystoneSecretRef"`
	// Secret ref to SSO credentials stored in a k8s secret, if applicable.
	SSOSecretRef *corev1.SecretReference `json:"ssoSecretRef"`
}

func NewNovaVMClient(config NovaVMClientConfig) VMClient {
	return &novaVMClient{
		config: config,
	}
}

func (c *novaVMClient) StartWithKubernetesSecrets(ctx context.Context, client client.Client) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("starting novaVMClient with Kubernetes secrets")
	var authenticatedHTTP = http.DefaultClient
	if c.config.SSOSecretRef != nil {
		var err error
		authenticatedHTTP, err = sso.Connector{Client: client}.
			FromSecretRef(ctx, *c.config.SSOSecretRef)
		if err != nil {
			log.Error(err, "failed to create SSO authenticated HTTP client")
			return err
		}
		log.Info("successfully created SSO authenticated HTTP client")
	}
	authenticatedKeystone, err := keystone.
		Connector{Client: client, HTTPClient: authenticatedHTTP}.
		FromSecretRef(ctx, c.config.KeystoneSecretRef)
	if err != nil {
		log.Error(err, "failed to create authenticated keystone client")
		return err
	}
	log.Info("successfully created authenticated keystone client")
	// Automatically fetch the nova endpoint from the keystone service catalog.
	provider := authenticatedKeystone.Client()
	serviceType := "compute"
	url, err := authenticatedKeystone.FindEndpoint(
		authenticatedKeystone.Availability(), serviceType,
	)
	if err != nil {
		log.Error(err, "failed to find nova endpoint in keystone service catalog")
		return err
	}
	log.Info("successfully found nova endpoint in keystone service catalog", "url", url)
	c.sc = &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       url,
		Type:           serviceType,
		// Since microversion 2.53, the hypervisor id and service id is a UUID.
		// We need that to find placement resource providers for hypervisors.
		Microversion: "2.53",
	}
	return nil
}

// GetCurrentVMSize returns the size of the VM with the given ID, or an
// error if the VM cannot be found.
func (c *novaVMClient) GetCurrentVMSize(ctx context.Context, vmID string) (map[hv1.ResourceName]resource.Quantity, error) {
	log := ctrl.LoggerFrom(ctx)
	if c.sc == nil {
		log.Error(nil, "nova service client not initialized yet")
		return nil, errors.New("nova service client not initialized yet")
	}
	var server struct {
		Flavor struct {
			RAM   int64 `json:"ram"`
			VCPUs int64 `json:"vcpus"`
		} `json:"flavor"`
	}
	err := servers.Get(ctx, c.sc, vmID).ExtractInto(&server)
	if err != nil {
		log.Error(err, "failed to get server details from nova", "vmID", vmID)
		return nil, err
	}
	size := map[hv1.ResourceName]resource.Quantity{
		hv1.ResourceCPU: *resource.
			NewQuantity(server.Flavor.VCPUs, resource.DecimalSI),
		hv1.ResourceMemory: *resource.
			NewQuantity(server.Flavor.RAM*1024*1024, resource.BinarySI),
	}
	log.Info("successfully retrieved VM size from nova", "vmID", vmID, "size", size)
	return size, nil
}
