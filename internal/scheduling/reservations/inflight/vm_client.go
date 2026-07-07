// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package inflight

import (
	"context"
	"log/slog"
	"net/http"

	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/cobaltcore-dev/cortex/pkg/sso"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	corev1 "k8s.io/api/core/v1"
)

type VMClient interface {
	// GetCurrentVMSize returns the size of the VM with the given ID,
	// or an error if the VM cannot be found.
	GetCurrentVMSize(ctx context.Context, vmID string) (map[hv1.ResourceName]resource.Quantity, error)
}

type novaVMClient struct {
	// sc is the service client for the OpenStack Nova API.
	sc *gophercloud.ServiceClient
}

type NovaVMClientConfig struct {
	// Secret ref to keystone credentials stored in a k8s secret.
	KeystoneSecretRef corev1.SecretReference `json:"keystoneSecretRef"`
	// Secret ref to SSO credentials stored in a k8s secret, if applicable.
	SSOSecretRef *corev1.SecretReference `json:"ssoSecretRef"`
}

func InitNovaVMClient(ctx context.Context, client client.Client, config NovaVMClientConfig) (VMClient, error) {
	var authenticatedHTTP = http.DefaultClient
	if config.SSOSecretRef != nil {
		var err error
		authenticatedHTTP, err = sso.Connector{Client: client}.
			FromSecretRef(ctx, *config.SSOSecretRef)
		if err != nil {
			return nil, err
		}
	}
	authenticatedKeystone, err := keystone.
		Connector{Client: client, HTTPClient: authenticatedHTTP}.
		FromSecretRef(ctx, config.KeystoneSecretRef)
	if err != nil {
		return nil, err
	}
	// Automatically fetch the nova endpoint from the keystone service catalog.
	provider := authenticatedKeystone.Client()
	serviceType := "compute"
	url, err := authenticatedKeystone.FindEndpoint(
		authenticatedKeystone.Availability(), serviceType,
	)
	if err != nil {
		return nil, err
	}
	slog.Info("using nova endpoint", "url", url)
	sc := &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       url,
		Type:           serviceType,
		// Since microversion 2.53, the hypervisor id and service id is a UUID.
		// We need that to find placement resource providers for hypervisors.
		Microversion: "2.53",
	}
	return &novaVMClient{sc: sc}, nil
}

// GetCurrentVMSize returns the size of the VM with the given ID, or an
// error if the VM cannot be found.
func (c *novaVMClient) GetCurrentVMSize(ctx context.Context, vmID string) (map[hv1.ResourceName]resource.Quantity, error) {
	var server struct {
		Flavor struct {
			RAM   int64 `json:"ram"`
			VCPUs int64 `json:"vcpus"`
		} `json:"flavor"`
	}
	err := servers.Get(ctx, c.sc, vmID).ExtractInto(&server)
	if err != nil {
		return nil, err
	}
	return map[hv1.ResourceName]resource.Quantity{
		hv1.ResourceCPU: *resource.
			NewQuantity(server.Flavor.VCPUs, resource.DecimalSI),
		hv1.ResourceMemory: *resource.
			NewQuantity(server.Flavor.RAM*1024*1024, resource.BinarySI),
	}, nil
}
